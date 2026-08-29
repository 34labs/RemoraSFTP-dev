package protocol

import (
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

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type stringsBuilder = strings.Builder

// genKey creates a fresh ed25519 keypair and returns the ssh.PublicKey plus
// the PEM-encoded OpenSSH private key.
func genKey(t *testing.T) (ssh.PublicKey, []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(block)
	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		t.Fatal(err)
	}
	_ = pub
	return signer.PublicKey(), pemBytes
}

// startTestSFTPServerDisk launches an in-process SSH+SFTP server backed by a
// temp directory, giving realistic file semantics for adapter tests.
func startTestSFTPServerDisk(t *testing.T) (hostport string, hostKey ssh.PublicKey, cleanup func()) {
	t.Helper()
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "seed.txt"), []byte("seed content"), 0o644)

	pub, privPEM := genKey(t)
	_ = pub
	signer, err := ssh.ParsePrivateKey(privPEM)
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

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hostport = listener.Addr().String()

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleDiskSFTP(conn, config, root)
		}
	}()

	return hostport, hostKey, func() {
		listener.Close()
		<-closed
	}
}

// handleDiskSFTP serves SFTP from root, translating virtual paths (/x) to
// the temp directory (root/x).
func handleDiskSFTP(conn net.Conn, config *ssh.ServerConfig, root string) {
	sc, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		conn.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	handler := &fileBackend{root: root}
	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			newCh.Reject(ssh.UnknownChannelType, "unknown")
			continue
		}
		ch, requests, err := newCh.Accept()
		if err != nil {
			sc.Close()
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
			FileGet:  handler,
			FilePut:  handler,
			FileList: handler,
			FileCmd:  handler,
		})
		// Serve blocks until the client disconnects; then clean up the
		// whole SSH connection and this goroutine.
		_ = srv.Serve()
		ch.Close()
		sc.Close()
		conn.Close()
		return
	}
}

// fileBackend is a minimal sftp request handler backed by a real directory,
// translating virtual SFTP paths into paths under root.
type fileBackend struct {
	root string
	mu   sync.Mutex
}

func (b *fileBackend) real(p string) string {
	clean := filepath.Clean("/" + p)
	return filepath.Join(b.root, clean)
}

func (b *fileBackend) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	f, err := os.Open(b.real(r.Filepath))
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (b *fileBackend) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	p := b.real(r.Filepath)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (b *fileBackend) Filecmd(r *sftp.Request) error {
	p := b.real(r.Filepath)
	target := b.real(r.Target)
	switch r.Method {
	case "Setstat":
		return nil
	case "Rename":
		return os.Rename(p, target)
	case "Rmdir", "Remove":
		return os.RemoveAll(p)
	case "Mkdir":
		return os.Mkdir(p, 0o755)
	case "Link", "Symlink":
		return os.Symlink(p, target)
	case "Chmod":
		return os.Chmod(p, r.Attributes().FileMode())
	case "Chown":
		return nil
	}
	return nil
}

type listerAt []os.FileInfo

func (l listerAt) ListAt(ls []os.FileInfo, offset int64) (int, error) {
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

func (b *fileBackend) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	p := b.real(r.Filepath)
	switch r.Method {
	case "List":
		entries, err := os.ReadDir(p)
		if err != nil {
			return nil, err
		}
		var infos []os.FileInfo
		for _, e := range entries {
			info, err := e.Info()
			if err != nil {
				continue
			}
			infos = append(infos, info)
		}
		return listerAt(infos), nil
	case "Stat":
		info, err := os.Stat(p)
		if err != nil {
			return nil, os.ErrNotExist
		}
		return listerAt{info}, nil
	}
	return nil, nil
}
