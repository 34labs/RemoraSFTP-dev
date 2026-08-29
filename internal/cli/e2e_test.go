package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"remorasftp/internal/app"
	"remorasftp/internal/config"
	"remorasftp/internal/manager"
	"remorasftp/internal/transfers"
)

// This test runs a real RemoraSFTP engine (config, credentials, trust, manager)
// in a temp data dir against an in-process SSH/SFTP server, then performs a
// full file operation lifecycle.
func TestEndToEndEngineOperations(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SFTPBOX_DATA_DIR", dir)

	// SFTP server backed by a temp root.
	root := t.TempDir()
	host, port, hostKey, srvCleanup := startDiskSFTPServer(t, root)
	defer srvCleanup()

	eng, err := app.New()
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Shutdown()

	// Trust the host key explicitly (simulates the user reviewing the fingerprint).
	hp := net.JoinHostPort(host, port)
	if err := eng.Trust.TrustSSH(hp, hostKey); err != nil {
		t.Fatal(err)
	}

	// Create a connection profile with a password secret.
	portNum := 0
	for _, r := range port {
		portNum = portNum*10 + int(r-'0')
	}
	c, err := eng.Manager.SaveProfile(manager.ProfileInput{
		Name: "E2E", Protocol: config.ProtoSFTP, Host: host, Port: portNum,
		Username: "testuser", Auth: "password", Password: strPtr("testpass"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.HasSecret != true {
		t.Fatal("profile should record that a secret is stored")
	}

	// Secret must not appear on disk in plaintext.
	data, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if strings.Contains(string(data), "testpass") {
		t.Fatal("password leaked into config.json")
	}

	// Connect.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sess, err := eng.Manager.Connect(ctx, c.ID)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer eng.Manager.Disconnect(sess.ID)

	// List root (empty initially, aside from nothing).
	entries, err := eng.Manager.List(ctx, sess.ID, "/")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty root, got %d", len(entries))
	}

	// Upload via the transfer manager (streaming).
	localFile := filepath.Join(t.TempDir(), "upload.txt")
	if err := os.WriteFile(localFile, []byte("e2e upload content"), 0o600); err != nil {
		t.Fatal(err)
	}
	cl, err := eng.Manager.Client(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(localFile)
	job := eng.Transfer.EnqueueStreamUpload(sess.ID, "/upload.txt", "upload.txt", 19, f, cl.Capabilities())
	eng.Transfer.Wait(job.ID)
	if snap := eng.Transfer.Snapshot(job.ID); snap.Status != "completed" {
		t.Fatalf("upload job status = %s (%s)", snap.Status, snap.Error)
	}

	// Download via the transfer manager into another local file.
	dlDir := t.TempDir()
	open, part, err := newDLWriter(dlDir, "download.txt")
	if err != nil {
		t.Fatal(err)
	}
	dlJob := &transfers.Job{SessionID: sess.ID, RemotePath: "/upload.txt", Name: "download.txt"}
	j2 := eng.Transfer.EnqueueDownload(dlJob, func(ctx context.Context, off int64) (io.WriteCloser, error) {
		return open(ctx, off)
	})
	eng.Transfer.Wait(j2.ID)
	if snap := eng.Transfer.Snapshot(j2.ID); snap.Status != "completed" {
		t.Fatalf("download job status = %s (%s)", snap.Status, snap.Error)
	}
	got, _ := os.ReadFile(filepath.Join(dlDir, "download.txt"))
	if string(got) != "e2e upload content" {
		t.Fatalf("downloaded content = %q", string(got))
	}
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Fatal("partial temp file should be cleaned up after success")
	}

	// Make a directory and rename the upload.
	if err := eng.Manager.Mkdir(ctx, sess.ID, "/folder"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := eng.Manager.Rename(ctx, sess.ID, "/upload.txt", "/folder/moved.txt"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	entries, err = eng.Manager.List(ctx, sess.ID, "/folder")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.Name == "moved.txt" {
			found = true
		}
	}
	if !found {
		t.Fatal("renamed file not found in folder")
	}

	// Preview text of the moved file.
	data2, _, err := cl.ReadPartial(ctx, "/folder/moved.txt", 4096)
	if err != nil {
		t.Fatal(err)
	}
	if string(data2) != "e2e upload content" {
		t.Fatalf("preview = %q", data2)
	}

	// Delete (recursive folder).
	if err := eng.Manager.Remove(ctx, sess.ID, "/folder", true); err != nil {
		t.Fatalf("remove: %v", err)
	}
	entries, _ = eng.Manager.List(ctx, sess.ID, "/")
	if len(entries) != 0 {
		t.Fatalf("root should be empty after delete, got %d", len(entries))
	}
}

func strPtr(s string) *string { return &s }

// newDLWriter wraps transfers.LocalDownloadWriter.
func newDLWriter(dir, name string) (func(context.Context, int64) (io.WriteCloser, error), string, error) {
	return dlWriterFactory(dir, name)
}

// --- minimal in-process SFTP server (duplicated in spirit from protocol tests) ---

func startDiskSFTPServer(t *testing.T, root string) (host string, port string, hostKey ssh.PublicKey, cleanup func()) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_ = pub
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(block)
	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		t.Fatal(err)
	}
	hostKey = signer.PublicKey()

	config := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == "testuser" && string(pass) == "testpass" {
				return nil, nil
			}
			return nil, io.EOF
		},
	}
	config.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host, port, _ = net.SplitHostPort(ln.Addr().String())

	var wg sync.WaitGroup
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func(conn net.Conn) {
				defer wg.Done()
				sc, chans, reqs, err := ssh.NewServerConn(conn, config)
				if err != nil {
					return
				}
				defer sc.Close()
				go ssh.DiscardRequests(reqs)
				for newCh := range chans {
					if newCh.ChannelType() != "session" {
						newCh.Reject(ssh.UnknownChannelType, "unknown")
						continue
					}
					ch, requests, err := newCh.Accept()
					if err != nil {
						return
					}
					go func(ch ssh.Channel, in <-chan *ssh.Request) {
						for req := range in {
							ok := req.Type == "subsystem"
							_ = req.Reply(ok, nil)
						}
						ch.Close()
					}(ch, requests)
					srv := sftp.NewRequestServer(ch, sftp.Handlers{
						FileGet:  &e2eBackend{root: root},
						FilePut:  &e2eBackend{root: root},
						FileList: &e2eBackend{root: root},
						FileCmd:  &e2eBackend{root: root},
					})
					_ = srv.Serve()
					return
				}
			}(conn)
		}
	}()
	return host, port, hostKey, func() { ln.Close(); wg.Wait() }
}

type e2eBackend struct{ root string }

func (b *e2eBackend) real(p string) string {
	clean := filepath.Clean("/" + p)
	return filepath.Join(b.root, clean)
}
func (b *e2eBackend) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	return os.Open(b.real(r.Filepath))
}
func (b *e2eBackend) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	p := b.real(r.Filepath)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	return os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
}
func (b *e2eBackend) Filecmd(r *sftp.Request) error {
	p, target := b.real(r.Filepath), b.real(r.Target)
	switch r.Method {
	case "Rename":
		return os.Rename(p, target)
	case "Rmdir", "Remove":
		return os.RemoveAll(p)
	case "Mkdir":
		return os.Mkdir(p, 0o755)
	case "Chmod":
		return nil
	}
	return nil
}

type e2eLister []os.FileInfo

func (l e2eLister) ListAt(ls []os.FileInfo, offset int64) (int, error) {
	n := 0
	if offset >= int64(len(l)) {
		return 0, io.EOF
	}
	for n < len(ls) && offset+int64(n) < int64(len(l)) {
		ls[n] = l[offset+int64(n)]
		n++
	}
	if n < len(ls) {
		return n, io.EOF
	}
	return n, nil
}

func (b *e2eBackend) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	p := b.real(r.Filepath)
	if r.Method == "List" {
		entries, err := os.ReadDir(p)
		if err != nil {
			return nil, err
		}
		var infos []os.FileInfo
		for _, e := range entries {
			if info, err := e.Info(); err == nil {
				infos = append(infos, info)
			}
		}
		return e2eLister(infos), nil
	}
	info, err := os.Stat(p)
	if err != nil {
		return nil, os.ErrNotExist
	}
	return e2eLister{info}, nil
}

var _ = bytes.MinRead

func dlWriterFactory(dir, name string) (func(context.Context, int64) (io.WriteCloser, error), string, error) {
	return transfers.LocalDownloadWriter(dir, name)
}

type stubJob struct{ *transfers.Job }

func jobStub(sessionID, remote, name string) stubJob {
	return stubJob{Job: &transfers.Job{SessionID: sessionID, RemotePath: remote, Name: name}}
}
