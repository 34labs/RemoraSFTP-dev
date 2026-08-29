package protocol

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strings"
	"time"

	"remorasftp/internal/safepath"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type sftpClient struct {
	cfg     Config
	sshConn *ssh.Client
	sftp    *sftp.Client
	info    ServerInfo
}

func newSFTP(cfg Config) *sftpClient {
	return &sftpClient{cfg: cfg}
}

func (c *sftpClient) Capabilities() Capabilities {
	return Capabilities{
		Protocol:         "sftp",
		Encrypted:        true,
		UnixPermissions:  true,
		Chmod:            true,
		Chown:            false, // not exposed in UI yet
		Symlinks:         true,
		ServerSideRename: true,
		ServerSideCopy:   false,
		ResumeDownload:   true,
		ResumeUpload:     true,
		MaxPreviewBytes:  8 << 20,
	}
}

func (c *sftpClient) ServerInfo() ServerInfo { return c.info }

func (c *sftpClient) Connect(ctx context.Context) error {
	hostport := net.JoinHostPort(c.cfg.Host, fmt.Sprintf("%d", c.cfg.Port))

	var authMethods []ssh.AuthMethod
	switch c.cfg.Auth {
	case "password":
		if c.cfg.Password != "" {
			authMethods = append(authMethods, ssh.Password(c.cfg.Password))
		}
	case "key":
		auth, err := c.privateKeyAuth()
		if err != nil {
			return err
		}
		authMethods = append(authMethods, auth)
	case "keyagent":
		if a := agentAuth(); a != nil {
			authMethods = append(authMethods, a)
		}
	case "none":
		// no credentials
	default:
		return fmt.Errorf("unsupported SFTP auth method %q", c.cfg.Auth)
	}
	// Always offer passwordless "none" last? No - explicit only.

	sshCfg := &ssh.ClientConfig{
		User:            c.cfg.Username,
		Auth:            authMethods,
		Timeout:         20 * time.Second,
		HostKeyCallback: c.hostKeyCallback(hostport),
	}

	dialer := net.Dialer{Timeout: 20 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", hostport)
	if err != nil {
		return fmt.Errorf("connect %s: %w", hostport, err)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, hostport, sshCfg)
	if err != nil {
		conn.Close()
		var uhe *UnknownHostKeyError
		if errors.As(err, &uhe) {
			return uhe
		}
		// ssh package returns *ssh.ExitError for auth issues sometimes;
		// password failures surface as handshakes failing to authenticate.
		if strings.Contains(err.Error(), "unable to authenticate") ||
			strings.Contains(err.Error(), "handshake failed") {
			return &AuthError{Detail: err.Error()}
		}
		return fmt.Errorf("SSH handshake: %w", err)
	}
	client := ssh.NewClient(sshConn, chans, reqs)
	sc, err := sftp.NewClient(client, sftp.UseConcurrentReads(true), sftp.UseConcurrentWrites(true))
	if err != nil {
		client.Close()
		return fmt.Errorf("SFTP subsystem: %w", err)
	}
	c.sshConn = client
	c.sftp = sc

	start := c.cfg.StartDir
	if start == "" {
		if home, err := sc.Getwd(); err == nil && home != "" {
			start = home
		} else {
			start = "/"
		}
	}
	c.info = ServerInfo{
		Protocol:  "sftp",
		Host:      c.cfg.Host,
		Port:      c.cfg.Port,
		Username:  c.cfg.Username,
		StartDir:  safepath.Clean(start),
		Encrypted: true,
	}
	return nil
}

// hostKeyCallback enforces trust: unknown keys return UnknownHostKeyError so
// the user must explicitly trust the fingerprint.
func (c *sftpClient) hostKeyCallback(hostport string) ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if c.cfg.Trust == nil {
			return &UnknownHostKeyError{HostPort: hostport, Fingerprint: ssh.FingerprintSHA256(key), KeyType: key.Type()}
		}
		trusted, fp := c.cfg.Trust.CheckSSH(hostport, key)
		if trusted {
			return nil
		}
		return &UnknownHostKeyError{HostPort: hostport, Fingerprint: fp, KeyType: key.Type()}
	}
}

func (c *sftpClient) privateKeyAuth() (ssh.AuthMethod, error) {
	var signer ssh.Signer
	var err error
	if len(c.cfg.KeyPassphrase) > 0 {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(c.cfg.PrivateKeyPEM, c.cfg.KeyPassphrase)
	} else {
		signer, err = ssh.ParsePrivateKey(c.cfg.PrivateKeyPEM)
	}
	if err != nil {
		// Wrong passphrase / bad key are credential problems.
		return nil, &AuthError{Detail: "private key: " + err.Error()}
	}
	return ssh.PublicKeys(signer), nil
}

// agentAuth uses an SSH agent when one is available (SSH_AUTH_SOCK etc).
func agentAuth() ssh.AuthMethod {
	if conn, err := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK")); err == nil {
		return ssh.PublicKeysCallback(agent.NewClient(conn).Signers)
	}
	return nil
}

func (c *sftpClient) Close() error {
	var err error
	if c.sftp != nil {
		err = c.sftp.Close()
	}
	if c.sshConn != nil {
		if e := c.sshConn.Close(); e != nil && err == nil {
			err = e
		}
	}
	return err
}

func (c *sftpClient) List(ctx context.Context, dir string) ([]Entry, error) {
	dir = safepath.Clean(dir)
	infos, err := c.sftp.ReadDir(dir)
	if err != nil {
		return nil, mapSFTPErr(err)
	}
	out := make([]Entry, 0, len(infos))
	for _, fi := range infos {
		full := path.Join(dir, fi.Name())
		e := Entry{
			Name:     fi.Name(),
			Path:     full,
			Size:     fi.Size(),
			ModTime:  fi.ModTime(),
			MIMEType: GuessMIME(fi.Name()),
		}
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			e.Type = EntrySymlink
			e.IsSymlink = true
			if tgt, err := c.sftp.ReadLink(full); err == nil {
				e.LinkTarget = tgt
			}
			// Resolve stat of target for size/type display.
			if st, err := c.sftp.Stat(full); err == nil {
				applyStatMode(&e, st)
			}
		case fi.IsDir():
			e.Type = EntryDir
		case fi.Mode().IsRegular():
			e.Type = EntryFile
		default:
			e.Type = EntryOther
		}
		e.Mode = uint32(fi.Mode().Perm())
		e.Permissions = PermString(uint32(fi.Mode().Perm()))
		if sys, ok := fi.Sys().(*sftp.FileStat); ok {
			e.Owner = fmt.Sprintf("%d", sys.UID)
			e.Group = fmt.Sprintf("%d", sys.GID)
		}
		out = append(out, e)
	}
	return out, nil
}

func applyStatMode(e *Entry, fi os.FileInfo) {
	if fi.IsDir() && e.Type != EntrySymlink {
		e.Type = EntryDir
	} else if fi.Mode().IsRegular() {
		e.Type = EntryFile
	}
	e.Size = fi.Size()
	e.ModTime = fi.ModTime()
}

func (c *sftpClient) Stat(ctx context.Context, p string) (Entry, error) {
	p = safepath.Clean(p)
	fi, err := c.sftp.Stat(p)
	if err != nil {
		return Entry{}, mapSFTPErr(err)
	}
	e := Entry{
		Name:        path.Base(p),
		Path:        p,
		Size:        fi.Size(),
		ModTime:     fi.ModTime(),
		MIMEType:    GuessMIME(path.Base(p)),
		Mode:        uint32(fi.Mode().Perm()),
		Permissions: PermString(uint32(fi.Mode().Perm())),
	}
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		e.Type = EntrySymlink
		e.IsSymlink = true
		if tgt, err := c.sftp.ReadLink(p); err == nil {
			e.LinkTarget = tgt
		}
	case fi.IsDir():
		e.Type = EntryDir
	default:
		e.Type = EntryFile
	}
	return e, nil
}

func (c *sftpClient) Mkdir(ctx context.Context, p string) error {
	return mapSFTPErr(c.sftp.Mkdir(safepath.Clean(p)))
}

func (c *sftpClient) Remove(ctx context.Context, p string, recursive bool) error {
	p = safepath.Clean(p)
	if !recursive {
		if err := c.sftp.Remove(p); err != nil {
			return mapSFTPErr(err)
		}
		return nil
	}
	return mapSFTPErr(c.sftp.RemoveAll(p))
}

func (c *sftpClient) Rename(ctx context.Context, from, to string) error {
	return mapSFTPErr(c.sftp.Rename(safepath.Clean(from), safepath.Clean(to)))
}

func (c *sftpClient) Chmod(ctx context.Context, p string, mode os.FileMode) error {
	return mapSFTPErr(c.sftp.Chmod(safepath.Clean(p), mode))
}

func (c *sftpClient) Upload(ctx context.Context, dst string, r io.Reader, startOffset int64, prog ProgressFunc) error {
	dst = safepath.Clean(dst)
	f, err := c.sftp.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if startOffset > 0 {
		// Resume: open existing and seek to offset.
		f, err = c.sftp.OpenFile(dst, os.O_WRONLY)
		if err == nil {
			if _, serr := f.Seek(startOffset, io.SeekStart); serr != nil {
				f.Close()
				return mapSFTPErr(serr)
			}
		}
	}
	if err != nil {
		return mapSFTPErr(err)
	}
	defer f.Close()
	pr := &progressReader{r: r, fn: prog, base: startOffset}
	_, err = io.Copy(f, pr)
	if err != nil {
		return mapSFTPErr(err)
	}
	return mapSFTPErr(f.Close())
}

func (c *sftpClient) Download(ctx context.Context, src string, w io.Writer, startOffset int64, prog ProgressFunc) error {
	src = safepath.Clean(src)
	f, err := c.sftp.Open(src)
	if err != nil {
		return mapSFTPErr(err)
	}
	defer f.Close()
	if startOffset > 0 {
		if _, err := f.Seek(startOffset, io.SeekStart); err != nil {
			return mapSFTPErr(err)
		}
	}
	pr := &progressReader{r: f, fn: prog, base: startOffset}
	_, err = io.Copy(w, pr)
	return mapSFTPErr(err)
}

func (c *sftpClient) ReadPartial(ctx context.Context, p string, maxBytes int64) ([]byte, bool, error) {
	p = safepath.Clean(p)
	f, err := c.sftp.Open(p)
	if err != nil {
		return nil, false, mapSFTPErr(err)
	}
	defer f.Close()
	buf := make([]byte, maxBytes+1)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, false, mapSFTPErr(err)
	}
	truncated := int64(n) > maxBytes
	if truncated {
		n = int(maxBytes)
	}
	// Trim trailing NULs defensively.
	return bytes.TrimRight(buf[:n], "\x00"), truncated, nil
}

func mapSFTPErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "does not exist") {
		return fmt.Errorf("remote path not found: %w", err)
	}
	if errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("permission denied on server: %w", err)
	}
	return err
}

type progressReader struct {
	r    io.Reader
	fn   ProgressFunc
	base int64
	n    int64
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.n += int64(n)
	if p.fn != nil {
		p.fn(p.base + p.n)
	}
	return n, err
}
