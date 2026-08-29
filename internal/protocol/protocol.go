// Package protocol defines the high-level file-management interface used by
// the whole application and provides adapters for FTP, FTPS, and SFTP.
//
// Nothing above this package imports a concrete protocol library. Capability
// differences between protocols are exposed explicitly through Capabilities
// rather than being papered over.
package protocol

import (
	"context"
	"errors"
	"io"
	"mime"
	"os"
	"path"
	"strings"
	"time"

	"remorasftp/internal/config"
	"remorasftp/internal/trust"
)

// EntryType classifies a remote filesystem entry.
type EntryType string

const (
	EntryFile    EntryType = "file"
	EntryDir     EntryType = "dir"
	EntrySymlink EntryType = "symlink"
	EntryOther   EntryType = "other"
)

// Entry is a protocol-neutral filesystem item.
type Entry struct {
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	Type        EntryType `json:"type"`
	Size        int64     `json:"size"`
	ModTime     time.Time `json:"modTime,omitempty"`
	Mode        uint32    `json:"mode,omitempty"`        // unix mode bits where available
	Permissions string    `json:"permissions,omitempty"` // rwxrwxrwx display
	Owner       string    `json:"owner,omitempty"`
	Group       string    `json:"group,omitempty"`
	IsSymlink   bool      `json:"isSymlink"`
	LinkTarget  string    `json:"linkTarget,omitempty"`
	MIMEType    string    `json:"mimeType,omitempty"`
}

// Capabilities describes what the connected protocol/server supports. The UI
// and transfer manager use these to enable or hide actions instead of failing
// late.
type Capabilities struct {
	Protocol         config.Protocol `json:"protocol"`
	Encrypted        bool            `json:"encrypted"`
	UnixPermissions  bool            `json:"unixPermissions"`
	Chmod            bool            `json:"chmod"`
	Chown            bool            `json:"chown"`
	Symlinks         bool            `json:"symlinks"`
	ServerSideRename bool            `json:"serverSideRename"`
	ServerSideCopy   bool            `json:"serverSideCopy"`
	ResumeDownload   bool            `json:"resumeDownload"`
	ResumeUpload     bool            `json:"resumeUpload"`
	// MaxPreviewBytes is the largest text payload the previewer will fetch.
	MaxPreviewBytes int64 `json:"maxPreviewBytes"`
}

// ServerInfo describes the connected remote.
type ServerInfo struct {
	Protocol  config.Protocol `json:"protocol"`
	Product   string          `json:"product,omitempty"` // server software banner where available
	Host      string          `json:"host"`
	Port      int             `json:"port"`
	Username  string          `json:"username"`
	StartDir  string          `json:"startDir,omitempty"`
	Encrypted bool            `json:"encrypted"`
}

// ProgressFunc is called periodically with cumulative bytes transferred.
type ProgressFunc func(transferred int64)

// Client is the common high-level interface every protocol adapter implements.
type Client interface {
	Connect(ctx context.Context) error
	Close() error
	Capabilities() Capabilities
	ServerInfo() ServerInfo

	List(ctx context.Context, dir string) ([]Entry, error)
	Stat(ctx context.Context, p string) (Entry, error)
	Mkdir(ctx context.Context, p string) error
	Remove(ctx context.Context, p string, recursive bool) error
	Rename(ctx context.Context, from, to string) error
	Chmod(ctx context.Context, p string, mode os.FileMode) error

	// Upload streams a local file (or any reader) to the remote path.
	// startOffset > 0 resumes a previously interrupted upload where the
	// protocol supports it.
	Upload(ctx context.Context, dst string, r io.Reader, startOffset int64, prog ProgressFunc) error
	// Download streams a remote file to a writer, starting at startOffset
	// for resumable protocols.
	Download(ctx context.Context, src string, w io.Writer, startOffset int64, prog ProgressFunc) error
	// ReadPartial fetches up to maxBytes for the previewer; truncated is
	// true when more data exists.
	ReadPartial(ctx context.Context, p string, maxBytes int64) (data []byte, truncated bool, err error)
}

// Config is everything an adapter needs to connect. Secrets are passed in
// memory only and never logged.
type Config struct {
	Protocol config.Protocol
	Host     string
	Port     int
	Username string
	Auth     config.AuthMethod

	Password      string
	PrivateKeyPEM []byte
	KeyPassphrase []byte

	StartDir string

	// FTPS
	FTPSImplicit         bool
	TLSVerify            bool
	TLSPinnedFingerprint string

	// SFTP
	KeepAlive time.Duration

	// FTP
	PassiveMode bool
	Encoding    string

	// Trust stores identity decisions (SSH host keys / TLS pins).
	Trust *trust.Store
}

// UnknownHostKeyError indicates an SFTP server presented a host key that has
// not been explicitly trusted. The UI must show the fingerprint and require a
// trust decision; the key is never accepted automatically.
type UnknownHostKeyError struct {
	HostPort    string
	Fingerprint string
	KeyType     string
}

func (e *UnknownHostKeyError) Error() string {
	return "untrusted SSH host key for " + e.HostPort + " (" + e.Fingerprint + ")"
}

// CertVerificationError indicates an FTPS server certificate failed
// verification. When Fingerprint is set, the UI may offer to pin it
// explicitly, exactly like an SSH host key.
type CertVerificationError struct {
	HostPort    string
	Fingerprint string
	Subject     string
	Detail      string
}

func (e *CertVerificationError) Error() string {
	return "TLS certificate verification failed for " + e.HostPort + ": " + e.Detail
}

// AuthError signals rejected credentials.
type AuthError struct{ Detail string }

func (e *AuthError) Error() string { return "authentication failed: " + e.Detail }

// AsUnknownHostKey extracts an UnknownHostKeyError if present.
func AsUnknownHostKey(err error) (*UnknownHostKeyError, bool) {
	var u *UnknownHostKeyError
	if errors.As(err, &u) {
		return u, true
	}
	return nil, false
}

// AsCertError extracts a CertVerificationError if present.
func AsCertError(err error) (*CertVerificationError, bool) {
	var c *CertVerificationError
	if errors.As(err, &c) {
		return c, true
	}
	return nil, false
}

// Open constructs (but does not connect) the adapter for the given protocol.
func Open(cfg Config) (Client, error) {
	switch cfg.Protocol {
	case config.ProtoSFTP:
		return newSFTP(cfg), nil
	case config.ProtoFTP, config.ProtoFTPS:
		return newFTP(cfg), nil
	default:
		return nil, errors.New("unsupported protocol: " + string(cfg.Protocol))
	}
}

// DefaultPort returns the conventional port for a protocol.
func DefaultPort(p config.Protocol) int {
	switch p {
	case config.ProtoSFTP:
		return 22
	case config.ProtoFTPS:
		return 21 // explicit FTPS shares the FTP port; implicit uses 990
	default:
		return 21
	}
}

// GuessMIME resolves a MIME type from a filename, with extra text formats the
// stdlib does not know. Unknown types resolve to application/octet-stream.
func GuessMIME(name string) string {
	ext := strings.ToLower(path.Ext(name))
	custom := map[string]string{
		".md":         "text/markdown",
		".markdown":   "text/markdown",
		".toml":       "application/toml",
		".yaml":       "application/yaml",
		".yml":        "application/yaml",
		".csv":        "text/csv",
		".tsv":        "text/tab-separated-values",
		".log":        "text/plain",
		".sh":         "application/x-sh",
		".bash":       "application/x-sh",
		".zsh":        "application/x-sh",
		".go":         "text/x-go",
		".rs":         "text/x-rust",
		".ts":         "text/x-typescript",
		".tsx":        "text/x-tsx",
		".jsx":        "text/x-jsx",
		".vue":        "text/x-vue",
		".proto":      "text/x-protobuf",
		".env":        "text/plain",
		".gitignore":  "text/plain",
		".dockerfile": "text/x-dockerfile",
	}
	if t, ok := custom[ext]; ok {
		return t
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	switch strings.TrimPrefix(ext, ".") {
	case "dockerfile", "makefile":
		return "text/plain"
	}
	base := strings.ToLower(path.Base(name))
	switch base {
	case "dockerfile", "makefile", "license", "readme":
		return "text/plain"
	}
	return "application/octet-stream"
}

// PermString renders unix permission bits as an rwxrwxrwx string.
func PermString(mode uint32) string {
	if mode == 0 {
		return ""
	}
	const bits = "rwxrwxrwx"
	var b strings.Builder
	for i := 0; i < 9; i++ {
		if mode&(1<<uint(8-i)) != 0 {
			b.WriteByte(bits[i])
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}
