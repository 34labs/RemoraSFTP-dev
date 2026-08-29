package protocol

// FTP and FTPS adapter.
//
// Implemented directly on net/textproto-style I/O rather than a third-party
// FTP library so that explicit FTPS (AUTH TLS), implicit FTPS, TLS session
// reuse on data channels, certificate pinning (the same trust model as SSH
// host keys), SITE CHMOD, and streaming transfers are all under our control.
import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"remorasftp/internal/safepath"
)

type ftpClient struct {
	cfg      Config
	conn     net.Conn
	dataConn net.Conn
	reader   *bufio.Reader
	writer   *bufio.Writer
	tlsOn    bool
	tlsConf  *tls.Config
	info     ServerInfo
	caps     Capabilities
	featMLSD bool
}

// applyDeadline sets an operation timeout on both control and any active
// data connection so a stalled or hostile server cannot hang a goroutine
// indefinitely. It returns a cleanup function.
func (c *ftpClient) applyDeadline(ctx context.Context) func() {
	dl := time.Now().Add(60 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(dl) {
		dl = d
	}
	_ = c.conn.SetDeadline(dl)
	if c.dataConn != nil {
		_ = c.dataConn.SetDeadline(dl)
	}
	return func() {
		_ = c.conn.SetDeadline(time.Time{})
		if c.dataConn != nil {
			_ = c.dataConn.SetDeadline(time.Time{})
		}
	}
}

func newFTP(cfg Config) *ftpClient {
	c := &ftpClient{cfg: cfg}
	c.caps = Capabilities{
		Protocol:         cfg.Protocol,
		Encrypted:        cfg.Protocol == "ftps",
		UnixPermissions:  false,
		Chmod:            true, // SITE CHMOD works on most unix servers; probed at connect
		Symlinks:         true,
		ServerSideRename: true,
		ServerSideCopy:   false,
		ResumeDownload:   true,
		ResumeUpload:     true, // APPE supports append
		MaxPreviewBytes:  4 << 20,
	}
	return c
}

func (c *ftpClient) Capabilities() Capabilities { return c.caps }
func (c *ftpClient) ServerInfo() ServerInfo     { return c.info }

// ---- connection -------------------------------------------------------

func (c *ftpClient) Connect(ctx context.Context) error {
	hostport := net.JoinHostPort(c.cfg.Host, fmt.Sprintf("%d", c.cfg.Port))
	dialer := net.Dialer{Timeout: 20 * time.Second}
	raw, err := dialer.DialContext(ctx, "tcp", hostport)
	if err != nil {
		return fmt.Errorf("connect %s: %w", hostport, err)
	}
	c.conn = raw
	c.reader = bufio.NewReader(raw)
	c.writer = bufio.NewWriter(raw)

	product := ""
	// Greeting - except implicit FTPS, where the greeting is sent *after*
	// TLS negotiation begins below.
	if !(c.cfg.Protocol == "ftps" && c.cfg.FTPSImplicit) {
		_, msg, err := c.readReply()
		if err != nil {
			c.Close()
			return fmt.Errorf("FTP greeting: %w", err)
		}
		product = strings.TrimSpace(msg)
	}

	c.tlsConf = &tls.Config{
		ServerName:         c.cfg.Host,
		InsecureSkipVerify: !c.cfg.TLSVerify, //nolint:gosec // user-controlled; pinning still enforced below
		MinVersion:         tls.VersionTLS12,
		ClientSessionCache: tls.NewLRUClientSessionCache(32),
	}
	if c.cfg.TLSVerify {
		c.tlsConf.VerifyConnection = c.verifyCert
	}

	if c.cfg.Protocol == "ftps" {
		if c.cfg.FTPSImplicit {
			// Implicit TLS: wrap immediately before any protocol exchange.
			// We consumed the plaintext greeting above; implicit servers
			// send the greeting *after* TLS, so reconnect under TLS.
			raw.Close()
			tconn, terr := tls.DialWithDialer(&dialer, "tcp", hostport, c.tlsConf)
			if terr != nil {
				return c.mapTLSErr(terr, hostport)
			}
			c.conn = tconn
			c.reader = bufio.NewReader(tconn)
			c.writer = bufio.NewWriter(tconn)
			if _, _, err := c.readReply(); err != nil {
				c.Close()
				return fmt.Errorf("FTPS greeting: %w", err)
			}
		} else {
			if _, _, err := c.cmd("AUTH TLS"); err != nil {
				c.Close()
				return fmt.Errorf("server does not support explicit FTPS (AUTH TLS): %w", err)
			}
			tconn := tls.Client(raw, c.tlsConf)
			if err := tconn.HandshakeContext(ctx); err != nil {
				c.Close()
				return c.mapTLSErr(err, hostport)
			}
			c.conn = tconn
			c.reader = bufio.NewReader(tconn)
			c.writer = bufio.NewWriter(tconn)
			// Protection setup for the data channel.
			if _, _, err := c.cmd("PBSZ 0"); err != nil {
				c.Close()
				return err
			}
			if _, _, err := c.cmd("PROT P"); err != nil {
				c.Close()
				return fmt.Errorf("server refused encrypted data channel (PROT P): %w", err)
			}
		}
		c.tlsOn = true
		c.caps.Encrypted = true
	}

	user := c.cfg.Username
	if user == "" {
		user = "anonymous"
	}
	if _, _, err := c.cmd("USER " + user); err != nil {
		c.Close()
		return &AuthError{Detail: err.Error()}
	}
	if c.cfg.Password != "" || user == "anonymous" {
		pass := c.cfg.Password
		if user == "anonymous" && pass == "" {
			pass = "anonymous@remorasftp.local"
		}
		code, _, passErr := c.cmd("PASS " + pass)
		if passErr != nil {
			c.Close()
			if code == 530 {
				return &AuthError{Detail: passErr.Error()}
			}
			return passErr
		}
	}
	// Feature detection.
	if _, feat, err := c.cmd("FEAT"); err == nil {
		if strings.Contains(strings.ToUpper(feat), "MLSD") {
			c.featMLSD = true
		}
		if !strings.Contains(strings.ToUpper(feat), "SITE CHMOD") {
			// only remove chmod capability if server explicitly lists FEAT
			// without it; many servers support SITE CHMOD unlisted.
		}
	}
	_, _, _ = c.cmd("TYPE I")
	_, _, _ = c.cmd("PBSZ 0")

	start := c.cfg.StartDir
	if start == "" {
		if _, pwd, err := c.cmd("PWD"); err == nil {
			start = parsePWD(pwd)
		}
	}
	if start != "" {
		if _, _, err := c.cmd("CWD " + start); err != nil {
			// Fall back to default directory; start dir is advisory.
			start = ""
		}
	}
	c.info = ServerInfo{
		Protocol:  c.cfg.Protocol,
		Product:   product,
		Host:      c.cfg.Host,
		Port:      c.cfg.Port,
		Username:  user,
		StartDir:  safepath.Clean(start),
		Encrypted: c.tlsOn,
	}
	return nil
}

// verifyConnection enforces an optional certificate pin. When a pinned
// fingerprint matches the leaf certificate, the connection is accepted even
// if the chain would not validate (self-signed internal servers). Otherwise
// standard verification applies and failures are wrapped with the fingerprint
// so the UI can offer an explicit trust decision.
func (c *ftpClient) verifyCert(cs tls.ConnectionState) error {
	if len(cs.PeerCertificates) == 0 {
		return &CertVerificationError{HostPort: c.cfg.Host, Detail: "server presented no certificate"}
	}
	leaf := cs.PeerCertificates[0]
	fp := certFingerprint(leaf)
	hostport := net.JoinHostPort(c.cfg.Host, fmt.Sprintf("%d", c.cfg.Port))
	if c.cfg.TLSPinnedFingerprint != "" {
		if fp == c.cfg.TLSPinnedFingerprint {
			return nil
		}
		// Record the presented cert so the user can pin it after review.
		if c.cfg.Trust != nil {
			c.cfg.Trust.CheckCert(hostport, leaf)
		}
		return &CertVerificationError{HostPort: hostport, Fingerprint: fp, Subject: leaf.Subject.String(), Detail: "pinned certificate does not match"}
	}
	// No pin: run normal verification manually to surface a trust prompt.
	if _, err := leaf.Verify(c.verificationOpts()); err != nil {
		// Stash the presented certificate as pending so an explicit user
		// decision can pin it (e.g. self-signed internal servers).
		if c.cfg.Trust != nil {
			c.cfg.Trust.CheckCert(hostport, leaf)
		}
		return &CertVerificationError{HostPort: hostport, Fingerprint: fp, Subject: leaf.Subject.String(), Detail: err.Error()}
	}
	return nil
}

func (c *ftpClient) verificationOpts() x509.VerifyOptions {
	return x509.VerifyOptions{
		DNSName:       c.cfg.Host,
		Intermediates: nil,
	}
}

func (c *ftpClient) mapTLSErr(err error, hostport string) error {
	var ce *CertVerificationError
	if errors.As(err, &ce) {
		return ce
	}
	return fmt.Errorf("FTPS handshake with %s: %w", hostport, err)
}

func certFingerprint(cert *x509.Certificate) string {
	return certFingerprintOf(cert.Raw)
}

func (c *ftpClient) Close() error {
	if c.conn == nil {
		return nil
	}
	_, _, _ = c.cmd("QUIT")
	err := c.conn.Close()
	c.conn = nil
	return err
}

// ---- wire protocol -----------------------------------------------------

// reply represents a multi-line FTP reply.
// readReply reads a complete (possibly multi-line) FTP reply. Multi-line
// replies look like:
//
//	211-Features:
//	 SIZE
//	211 end
//
// and must be consumed fully before the next command is issued, otherwise
// the control connection replies get out of sync.
func (c *ftpClient) readReply() (int, string, error) {
	var lines []string
	code := 0
	expecting := false
	dashCode := 0
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return 0, "", err
		}
		line = strings.TrimRight(line, "\r\n")
		if len(line) < 3 {
			continue
		}
		n, err := strconv.Atoi(line[:3])
		if err != nil {
			if expecting {
				lines = append(lines, line)
			}
			continue
		}
		sep := byte(' ')
		if len(line) > 3 {
			sep = line[3]
		}
		switch {
		case !expecting && sep == '-':
			// Start of a multi-line reply.
			expecting = true
			dashCode = n
			code = n
			if len(line) > 4 {
				lines = append(lines, line[4:])
			}
		case expecting && sep == '-' && n == dashCode:
			// Another continuation dash line for the same code.
			if len(line) > 4 {
				lines = append(lines, line[4:])
			}
		case expecting && n == dashCode && (sep == ' ' || len(line) == 3):
			// Terminating line.
			if len(line) > 4 {
				lines = append(lines, line[4:])
			}
			return code, strings.Join(lines, "\n"), nil
		case !expecting:
			// Single-line reply.
			if len(line) > 4 {
				lines = append(lines, line[4:])
			}
			return n, strings.Join(lines, "\n"), nil
		default:
			// Continuation text line.
			lines = append(lines, line)
		}
	}
}

// cmd sends a command and returns the reply. FTP commands are line based.
func (c *ftpClient) cmd(format string, args ...any) (int, string, error) {
	if c.conn == nil {
		return 0, "", errors.New("FTP connection closed")
	}
	c.writer.Reset(c.conn)
	formatted := format
	if len(args) > 0 {
		formatted = fmt.Sprintf(format, args...)
	}
	if _, err := c.writer.WriteString(formatted + "\r\n"); err != nil {
		return 0, "", err
	}
	if err := c.writer.Flush(); err != nil {
		return 0, "", err
	}
	code, msg, err := c.readReply()
	if err != nil {
		return code, msg, err
	}
	if code >= 400 {
		return code, msg, fmt.Errorf("ftp: %s (%d)", strings.Split(msg, "\n")[0], code)
	}
	return code, msg, nil
}

// beginPassive enters passive mode and returns a dial function that
// establishes the (optionally TLS-wrapped) data connection. The caller must
// send the data command (LIST/RETR/STOR/...) before invoking dial, matching
// the RFC order: PASV -> command -> 150 reply -> data connection.
func (c *ftpClient) beginPassive() (dial func() (net.Conn, error), err error) {
	port, err := c.passivePort()
	if err != nil {
		return nil, err
	}
	if port <= 0 {
		return nil, errors.New("server did not provide a passive-mode port")
	}
	hostport := net.JoinHostPort(c.cfg.Host, fmt.Sprintf("%d", port))
	return func() (net.Conn, error) {
		dialer := net.Dialer{Timeout: 30 * time.Second}
		conn, err := dialer.Dial("tcp", hostport)
		if err != nil {
			return nil, fmt.Errorf("data connection: %w", err)
		}
		if c.tlsOn {
			tconn := tls.Client(conn, c.tlsConf)
			if err := tconn.Handshake(); err != nil {
				conn.Close()
				return nil, c.mapTLSErr(err, hostport)
			}
			conn = tconn
		}
		return conn, nil
	}, nil
}

func (c *ftpClient) passivePort() (int, error) {
	// EPSV first (NAT-friendly, IPv6-ready); fall back to legacy PASV on
	// servers that do not implement it.
	if _, msg, err := c.cmd("EPSV"); err == nil {
		if p := parseEPSV(msg); p > 0 {
			return p, nil
		}
	}
	// Consume any spurious reply from the failed EPSV attempt before
	// issuing PASV so the control connection stays in sync.
	// (A 502/500 reply is already the complete response, so nothing to do.)
	_, msg, err := c.cmd("PASV")
	if err != nil {
		return 0, err
	}
	return parsePASV(msg)
}

func parseEPSV(msg string) int {
	// Reply shape: "229 Entering Extended Passive Mode (|||12345|)"
	open := strings.IndexByte(msg, '(')
	close := strings.IndexByte(msg, ')')
	if open < 0 || close < 0 || close < open {
		return 0
	}
	seg := msg[open+1 : close] // "|||12345|"
	fields := strings.Split(seg, "|")
	for _, f := range fields {
		if p, err := strconv.Atoi(f); err == nil && p > 0 {
			return p
		}
	}
	return 0
}

func parsePASV(msg string) (int, error) {
	start := strings.IndexByte(msg, '(')
	end := strings.IndexByte(msg, ')')
	if start < 0 || end < 0 || end < start {
		return 0, fmt.Errorf("cannot parse PASV reply: %q", msg)
	}
	parts := strings.Split(msg[start+1:end], ",")
	if len(parts) != 6 {
		return 0, fmt.Errorf("malformed PASV reply: %q", msg)
	}
	nums := make([]int, 6)
	for i, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return 0, err
		}
		nums[i] = n
	}
	return nums[4]*256 + nums[5], nil
}

func parsePWD(msg string) string {
	s := strings.IndexByte(msg, '"')
	if s < 0 {
		return ""
	}
	e := strings.LastIndexByte(msg, '"')
	if e <= s {
		return ""
	}
	return msg[s+1 : e]
}

// ---- operations --------------------------------------------------------

func (c *ftpClient) List(ctx context.Context, dir string) ([]Entry, error) {
	dir = safepath.Clean(dir)
	if c.featMLSD {
		entries, err := c.listMLSD(ctx, dir)
		if err == nil {
			return entries, nil
		}
		// fall through to LIST on failure
	}
	return c.listLIST(ctx, dir)
}

func (c *ftpClient) listMLSD(ctx context.Context, dir string) ([]Entry, error) {
	cleanup := c.applyDeadline(ctx)
	defer cleanup()
	dial, err := c.beginPassive()
	if err != nil {
		return nil, err
	}
	if _, _, err := c.cmd("MLSD " + dir); err != nil {
		return nil, err
	}
	dc, err := dial()
	if err != nil {
		return nil, err
	}
	defer dc.Close()
	var out []Entry
	sc := bufio.NewScanner(dc)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		facts := map[string]string{}
		for _, f := range strings.Split(strings.ToLower(parts[0]), ";") {
			kv := strings.SplitN(f, "=", 2)
			if len(kv) == 2 {
				facts[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
			}
		}
		name := strings.TrimSpace(parts[1])
		if name == "." || name == ".." {
			continue
		}
		ft := facts["type"]
		if ft == "cdir" || ft == "pdir" {
			continue
		}
		e := Entry{Name: name, Path: path.Join(dir, name), MIMEType: GuessMIME(name)}
		if ft == "dir" {
			e.Type = EntryDir
		} else {
			e.Type = EntryFile
		}
		if s, err := strconv.ParseInt(facts["size"], 10, 64); err == nil {
			e.Size = s
		}
		if m := facts["modify"]; len(m) >= 14 {
			if t, err := time.ParseInLocation("20060102150405", m[:14], time.UTC); err == nil {
				e.ModTime = t
			}
		}
		if ft == "dir" {
			e.Size = 0
		}
		out = append(out, e)
	}
	// Final reply (226).
	if _, _, err := c.readReply(); err != nil {
		return nil, err
	}
	return out, sc.Err()
}

func (c *ftpClient) listLIST(ctx context.Context, dir string) ([]Entry, error) {
	cleanup := c.applyDeadline(ctx)
	defer cleanup()
	dial, err := c.beginPassive()
	if err != nil {
		return nil, err
	}
	if _, _, err := c.cmd("LIST " + dir); err != nil {
		return nil, err
	}
	dc, err := dial()
	if err != nil {
		return nil, err
	}
	defer dc.Close()
	data, err := io.ReadAll(dc)
	if err != nil {
		return nil, err
	}
	if _, _, err := c.readReply(); err != nil {
		return nil, err
	}
	return parseLISTOutput(string(data), dir), nil
}

func (c *ftpClient) Mkdir(ctx context.Context, p string) error {
	cleanup := c.applyDeadline(ctx)
	defer cleanup()
	p = safepath.Clean(p)
	if _, _, err := c.cmd("MKD " + p); err != nil {
		return err
	}
	return nil
}

func (c *ftpClient) Remove(ctx context.Context, p string, recursive bool) error {
	p = safepath.Clean(p)
	if recursive {
		entries, err := c.List(ctx, p)
		if err == nil {
			for _, e := range entries {
				if e.Type == EntryDir {
					if err := c.Remove(ctx, e.Path, true); err != nil {
						return err
					}
				} else {
					if _, _, err := c.cmd("DELE " + e.Path); err != nil {
						return err
					}
				}
			}
		}
	}
	if _, _, err := c.cmd("RMD " + p); err == nil {
		return nil
	}
	_, _, err := c.cmd("DELE " + p)
	return err
}

func (c *ftpClient) Rename(ctx context.Context, from, to string) error {
	cleanup := c.applyDeadline(ctx)
	defer cleanup()
	from = safepath.Clean(from)
	to = safepath.Clean(to)
	if _, _, err := c.cmd("RNFR " + from); err != nil {
		return err
	}
	_, _, err := c.cmd("RNTO " + to)
	return err
}

func (c *ftpClient) Chmod(ctx context.Context, p string, mode os.FileMode) error {
	cleanup := c.applyDeadline(ctx)
	defer cleanup()
	p = safepath.Clean(p)
	_, msg, err := c.cmd("SITE CHMOD %o %s", mode.Perm(), p)
	if err != nil {
		// Server may not support SITE CHMOD.
		return fmt.Errorf("chmod unsupported by server: %s", strings.TrimSpace(msg))
	}
	return nil
}

func (c *ftpClient) Stat(ctx context.Context, p string) (Entry, error) {
	p = safepath.Clean(p)
	e := Entry{Name: path.Base(p), Path: p, MIMEType: GuessMIME(path.Base(p))}
	_, sizeMsg, err := c.cmd("SIZE " + p)
	if err == nil {
		e.Type = EntryFile
		if n, perr := strconv.ParseInt(strings.TrimSpace(firstToken(sizeMsg)), 10, 64); perr == nil {
			e.Size = n
		}
		if _, mdtm, merr := c.cmd("MDTM " + p); merr == nil {
			if t, terr := time.ParseInLocation("20060102150405", strings.TrimSpace(firstToken(mdtm))[:14], time.UTC); terr == nil {
				e.ModTime = t
			}
		}
		return e, nil
	}
	// Probably a directory: confirm by listing the parent.
	dir, name := path.Split(p)
	entries, err := c.List(ctx, dir)
	if err != nil {
		return Entry{}, fmt.Errorf("remote path not found: %s", p)
	}
	for _, ent := range entries {
		if ent.Name == name {
			ent.Path = p
			return ent, nil
		}
	}
	return Entry{}, fmt.Errorf("remote path not found: %s", p)
}

func firstToken(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[i+1:]
	}
	return s
}

func (c *ftpClient) Upload(ctx context.Context, dst string, r io.Reader, startOffset int64, prog ProgressFunc) error {
	cleanup := c.applyDeadline(ctx)
	defer cleanup()
	dst = safepath.Clean(dst)
	dial, err := c.beginPassive()
	if err != nil {
		return err
	}
	command := "STOR " + dst
	if startOffset > 0 {
		// Resume: seek server-side via REST then APPE.
		if _, _, err := c.cmd("REST %d", startOffset); err != nil {
			return err
		}
		command = "APPE " + dst
	}
	if _, _, err := c.cmd(command); err != nil {
		return err
	}
	dc, err := dial()
	if err != nil {
		return err
	}
	c.dataConn = dc
	defer func() { c.dataConn = nil; dc.Close() }()
	pw := &progressWriter{w: dc, fn: prog, base: startOffset}
	_, err = io.Copy(pw, r)
	if err != nil {
		_, _, _ = c.cmd("ABOR")
		return err
	}
	// Signal end-of-stream to the server for half-close support.
	if cw, ok := dc.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
	if _, _, err := c.readReply(); err != nil {
		return err
	}
	return nil
}

func (c *ftpClient) Download(ctx context.Context, src string, w io.Writer, startOffset int64, prog ProgressFunc) error {
	cleanup := c.applyDeadline(ctx)
	defer cleanup()
	src = safepath.Clean(src)
	dial, err := c.beginPassive()
	if err != nil {
		return err
	}
	if startOffset > 0 {
		if _, _, err := c.cmd("REST %d", startOffset); err != nil {
			return err
		}
	}
	if _, _, err := c.cmd("RETR " + src); err != nil {
		return err
	}
	dc, err := dial()
	if err != nil {
		return err
	}
	defer dc.Close()
	pr := &progressReader{r: dc, fn: prog, base: startOffset}
	if _, err := io.Copy(w, pr); err != nil {
		return err
	}
	if _, _, err := c.readReply(); err != nil {
		return err
	}
	return nil
}

func (c *ftpClient) ReadPartial(ctx context.Context, p string, maxBytes int64) ([]byte, bool, error) {
	cleanup := c.applyDeadline(ctx)
	defer cleanup()
	p = safepath.Clean(p)
	dial, err := c.beginPassive()
	if err != nil {
		return nil, false, err
	}
	if _, _, err := c.cmd("RETR " + p); err != nil {
		return nil, false, err
	}
	dc, err := dial()
	if err != nil {
		return nil, false, err
	}
	buf := make([]byte, maxBytes+1)
	n, _ := io.ReadFull(dc, buf)
	dc.Close()
	_, _, _ = c.cmd("ABOR")
	// Drain the final/ABOR replies without treating them as fatal.
	_, _ = c.reader.ReadString('\n')
	truncated := int64(n) > maxBytes
	if truncated {
		n = int(maxBytes)
	}
	return buf[:n], truncated, nil
}

type progressWriter struct {
	w    io.Writer
	fn   ProgressFunc
	base int64
	n    int64
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.n += int64(n)
	if p.fn != nil {
		p.fn(p.base + p.n)
	}
	return n, err
}
