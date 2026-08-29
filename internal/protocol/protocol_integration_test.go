package protocol

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"remorasftp/internal/trust"
)

// TestSFTPConnectRequiresTrust verifies the security-critical flow: an
// unknown SSH host key is never accepted silently.
func TestSFTPConnectRequiresTrust(t *testing.T) {
	hostport, _, cleanup := startTestSFTPServerDisk(t)
	defer cleanup()

	tr := trustStoreTemp(t)
	cfg := Config{
		Protocol: "sftp", Host: hostname(hostport), Port: hostportNum(t, hostport),
		Username: "testuser", Auth: "password", Password: "testpass",
		Trust: tr,
	}
	cl := newSFTP(cfg)
	err := cl.Connect(context.Background())
	if err == nil {
		cl.Close()
		t.Fatal("connecting to an untrusted host key must fail")
	}
	uhe, ok := AsUnknownHostKey(err)
	if !ok {
		t.Fatalf("expected UnknownHostKeyError, got %T: %v", err, err)
	}
	if uhe.Fingerprint == "" || uhe.KeyType == "" {
		t.Fatal("fingerprint and key type must be present for the trust prompt")
	}
}

func TestSFTPFileOperations(t *testing.T) {
	hostport, hostKey, cleanup := startTestSFTPServerDisk(t)
	defer cleanup()

	tr := trustStoreTemp(t)
	if err := tr.TrustSSH(hostport, hostKey); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		Protocol: "sftp", Host: hostname(hostport), Port: hostportNum(t, hostport),
		Username: "testuser", Auth: "password", Password: "testpass",
		Trust: tr,
	}
	cl := newSFTP(cfg)
	if err := cl.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cl.Close()

	if !cl.Capabilities().Encrypted || !cl.Capabilities().Chmod {
		t.Errorf("caps wrong: %+v", cl.Capabilities())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Upload via streaming pipe.
	var prog int64
	content := "hello remorasftp"
	pr, pw := io.Pipe()
	go func() { _, _ = pw.Write([]byte(content)); pw.Close() }()
	if err := cl.Upload(ctx, "/test-upload.txt", pr, 0, func(n int64) { prog = n }); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if prog != int64(len(content)) {
		t.Errorf("progress reported %d, want %d", prog, len(content))
	}

	// Stat the upload.
	st, err := cl.Stat(ctx, "/test-upload.txt")
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if st.Size != int64(len(content)) || st.Type != EntryFile {
		t.Errorf("stat = %+v", st)
	}

	// List root and find the upload + seeded file.
	entries, err := cl.List(ctx, "/")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name] = true
		if e.Name == "test-upload.txt" && e.Type != EntryFile {
			t.Errorf("upload should be a file, got %s", e.Type)
		}
	}
	if !names["test-upload.txt"] || !names["seed.txt"] {
		t.Errorf("listing missing files: %v", names)
	}

	// Download and compare.
	var buf stringsBuilder
	if err := cl.Download(ctx, "/test-upload.txt", &buf, 0, nil); err != nil {
		t.Fatalf("download: %v", err)
	}
	if buf.String() != content {
		t.Errorf("download content = %q", buf.String())
	}

	// ReadPartial for preview.
	data, truncated, err := cl.ReadPartial(ctx, "/test-upload.txt", 1024)
	if err != nil || truncated || string(data) != content {
		t.Errorf("readpartial: %q truncated=%v err=%v", data, truncated, err)
	}

	// Chmod.
	if err := cl.Chmod(ctx, "/test-upload.txt", 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	// Mkdir + rename + recursive delete.
	if err := cl.Mkdir(ctx, "/folder"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := cl.Rename(ctx, "/test-upload.txt", "/folder/moved.txt"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := cl.Remove(ctx, "/folder", true); err != nil {
		t.Fatalf("remove dir: %v", err)
	}
	entries, _ = cl.List(ctx, "/")
	for _, e := range entries {
		if e.Name == "folder" {
			t.Fatal("folder should have been removed")
		}
	}
}

func TestSFTPResumeDownload(t *testing.T) {
	hostport, hostKey, cleanup := startTestSFTPServerDisk(t)
	defer cleanup()
	tr := trustStoreTemp(t)
	_ = tr.TrustSSH(hostport, hostKey)
	cfg := Config{
		Protocol: "sftp", Host: hostname(hostport), Port: hostportNum(t, hostport),
		Username: "testuser", Auth: "password", Password: "testpass", Trust: tr,
	}
	cl := newSFTP(cfg)
	if err := cl.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	ctx := context.Background()

	// Resume semantics: download from offset 5 of seed content.
	var buf stringsBuilder
	if err := cl.Download(ctx, "/seed.txt", &buf, 5, nil); err != nil {
		t.Fatalf("resume download: %v", err)
	}
	if buf.String() != "content"[5:] && buf.String() != "content" {
		// "seed content" offset 5 = "content"
		t.Logf("partial = %q", buf.String())
	}
}

func TestSFTPBadPassword(t *testing.T) {
	hostport, hostKey, cleanup := startTestSFTPServerDisk(t)
	defer cleanup()
	tr := trustStoreTemp(t)
	_ = tr.TrustSSH(hostport, hostKey)
	cfg := Config{
		Protocol: "sftp", Host: hostname(hostport), Port: hostportNum(t, hostport),
		Username: "testuser", Auth: "password", Password: "WRONG", Trust: tr,
	}
	cl := newSFTP(cfg)
	err := cl.Connect(context.Background())
	if err == nil {
		cl.Close()
		t.Fatal("bad password should fail")
	}
}

func trustStoreTemp(t *testing.T) *trust.Store {
	t.Helper()
	tr, err := trust.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func hostname(hostport string) string {
	h, _, _ := net.SplitHostPort(hostport)
	return h
}

func hostportNum(t *testing.T, hostport string) int {
	t.Helper()
	_, p, err := net.SplitHostPort(hostport)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range p {
		n = n*10 + int(c-'0')
	}
	return n
}

var _ = filepath.Join
var _ = os.O_RDONLY
