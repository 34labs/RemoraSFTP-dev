// Package manager owns connection profiles and live remote sessions. It is
// the single authority over protocol adapters: both the HTTP API and the CLI
// go through it, so they can never diverge in behavior.
package manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"remorasftp/internal/config"
	"remorasftp/internal/credentials"
	"remorasftp/internal/events"
	"remorasftp/internal/protocol"
	"remorasftp/internal/trust"

	"github.com/google/uuid"
)

// State of a live session.
type State string

const (
	StateDisconnected State = "disconnected"
	StateConnecting   State = "connecting"
	StateConnected    State = "connected"
	StateError        State = "error"
)

// Session is a live connection instance.
type Session struct {
	ID          string                `json:"id"`
	ConnID      string                `json:"connectionId"`
	ConnName    string                `json:"connectionName"`
	Protocol    config.Protocol       `json:"protocol"`
	Host        string                `json:"host"`
	State       State                 `json:"state"`
	Error       string                `json:"error,omitempty"`
	StartDir    string                `json:"startDir,omitempty"`
	CWD         string                `json:"cwd,omitempty"`
	ConnectedAt time.Time             `json:"connectedAt,omitempty"`
	Caps        protocol.Capabilities `json:"capabilities"`
	Server      protocol.ServerInfo   `json:"server"`

	client protocol.Client
}

// Manager coordinates profiles and sessions.
type Manager struct {
	mu       sync.RWMutex
	cfg      *config.Store
	creds    credentials.Provider
	trust    *trust.Store
	bus      *events.Bus
	sessions map[string]*Session
}

// New constructs a Manager.
func New(cfg *config.Store, creds credentials.Provider, trust *trust.Store, bus *events.Bus) *Manager {
	return &Manager{
		cfg:      cfg,
		creds:    creds,
		trust:    trust,
		bus:      bus,
		sessions: map[string]*Session{},
	}
}

// TrustPending persists an explicit user trust decision for a presented
// (but previously untrusted) SSH host key or TLS certificate. The fingerprint
// must match the artifact captured during the failed handshake.
func (m *Manager) TrustPending(hostport, fingerprint string) error {
	if err := m.trust.TrustPending(hostport, fingerprint); err != nil {
		return err
	}
	m.bus.Info(events.TypeInfo, "trust decision recorded for "+hostport)
	return nil
}

// CredentialBackend returns the name of the active secret storage backend.
func (m *Manager) CredentialBackend() string { return m.creds.Name() }

// ---- profile CRUD ------------------------------------------------------

// Profiles returns all connection profiles (secrets are never included).
func (m *Manager) Profiles() []*config.Connection { return m.cfg.Connections() }

// Profile returns one profile by ID.
func (m *Manager) Profile(id string) (*config.Connection, bool) { return m.cfg.Connection(id) }

// ProfileInput carries create/update data from the API/CLI.
type ProfileInput struct {
	ID           string
	Name         string
	Protocol     config.Protocol
	Host         string
	Port         int
	Username     string
	Auth         config.AuthMethod
	StartDir     string
	FTPSImplicit bool
	TLSVerify    *bool
	PassiveMode  *bool
	KeepAlive    int
	Encoding     string
	// Secret material. nil fields mean "leave unchanged" on update; an
	// empty string for a secret means "clear stored secret".
	Password      *string
	PrivateKeyPEM *string
	KeyPassphrase *string
}

// SaveProfile creates or updates a profile and writes any supplied secrets to
// the credential vault.
func (m *Manager) SaveProfile(in ProfileInput) (*config.Connection, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, errors.New("connection name is required")
	}
	if strings.TrimSpace(in.Host) == "" {
		return nil, errors.New("host is required")
	}
	if in.Protocol == "" {
		return nil, errors.New("protocol is required")
	}
	id := in.ID
	if id == "" {
		id = uuid.NewString()
	}
	existing, _ := m.cfg.Connection(id)
	c := &config.Connection{
		ID:       id,
		Name:     strings.TrimSpace(in.Name),
		Protocol: in.Protocol,
		Host:     strings.TrimSpace(in.Host),
		Port:     in.Port,
		Username: in.Username,
		Auth:     in.Auth,
		StartDir: in.StartDir,
		Settings: config.ConnectionSettings{
			FTPSImplicit:     in.FTPSImplicit,
			PassiveMode:      true,
			TLSVerify:        true,
			KeepAliveSeconds: in.KeepAlive,
			Encoding:         in.Encoding,
		},
	}
	if c.Port == 0 {
		c.Port = protocol.DefaultPort(c.Protocol)
		if c.Protocol == config.ProtoFTPS && in.FTPSImplicit {
			c.Port = 990
		}
	}
	if c.Auth == "" {
		if c.Protocol == config.ProtoSFTP {
			c.Auth = config.AuthPassword
		} else {
			c.Auth = config.AuthPassword
		}
	}
	if existing != nil {
		c.Settings = existing.Settings
		c.HasSecret = existing.HasSecret
		c.HasPrivateKey = existing.HasPrivateKey
	}
	if in.TLSVerify != nil {
		c.Settings.TLSVerify = *in.TLSVerify
	}
	if in.PassiveMode != nil {
		c.Settings.PassiveMode = *in.PassiveMode
	}
	c.Settings.FTPSImplicit = in.FTPSImplicit
	c.Settings.KeepAliveSeconds = in.KeepAlive
	c.Settings.Encoding = in.Encoding

	if err := m.cfg.Upsert(c); err != nil {
		return nil, err
	}

	// Secrets: only touch the vault when the caller supplied secret fields.
	ctx := context.Background()
	if in.Password != nil || in.PrivateKeyPEM != nil || in.KeyPassphrase != nil {
		cred, err := m.creds.Get(ctx, id)
		if err != nil && !errors.Is(err, credentials.ErrNotFound) {
			return nil, err
		}
		if in.Password != nil {
			cred.Password = *in.Password
		}
		if in.PrivateKeyPEM != nil {
			cred.PrivateKeyPEM = *in.PrivateKeyPEM
		}
		if in.KeyPassphrase != nil {
			cred.KeyPassphrase = *in.KeyPassphrase
		}
		cred.ConnectionID = id
		if cred.HasAnySecret() {
			if err := m.creds.Put(ctx, cred); err != nil {
				return nil, err
			}
		} else {
			_ = m.creds.Delete(ctx, id)
		}
		c.HasSecret = cred.Password != "" || cred.KeyPassphrase != ""
		c.HasPrivateKey = cred.PrivateKeyPEM != ""
		if err := m.cfg.Upsert(c); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// DuplicateProfile copies a profile under a new name (secrets are shared by
// copying the vault entry).
func (m *Manager) DuplicateProfile(id string) (*config.Connection, error) {
	src, ok := m.cfg.Connection(id)
	if !ok {
		return nil, fmt.Errorf("connection %q not found", id)
	}
	dst := *src
	dst.ID = uuid.NewString()
	dst.Name = src.Name + " (copy)"
	if err := m.cfg.Upsert(&dst); err != nil {
		return nil, err
	}
	// Copy secrets if present.
	if src.HasSecret || src.HasPrivateKey {
		ctx := context.Background()
		if cred, err := m.creds.Get(ctx, id); err == nil {
			cred.ConnectionID = dst.ID
			if err := m.creds.Put(ctx, cred); err == nil {
				dst.HasSecret = src.HasSecret
				dst.HasPrivateKey = src.HasPrivateKey
				_ = m.cfg.Upsert(&dst)
			}
		}
	}
	return &dst, nil
}

// DeleteProfile removes a profile, its sessions, and its stored secrets.
func (m *Manager) DeleteProfile(id string) error {
	m.mu.Lock()
	for sid, s := range m.sessions {
		if s.ConnID == id {
			_ = s.client.Close()
			delete(m.sessions, sid)
		}
	}
	m.mu.Unlock()
	_ = m.creds.Delete(context.Background(), id)
	return m.cfg.Delete(id)
}

// ---- sessions ----------------------------------------------------------

// TrustDecision carries an explicit user choice to trust a host key/cert.
type TrustDecision struct {
	HostPort    string `json:"hostPort"`
	Kind        string `json:"kind"` // "ssh" | "tls"
	Fingerprint string `json:"fingerprint"`
}

// TrustedHosts lists stored trust entries.
func (m *Manager) TrustedHosts() []*trust.Entry { return m.trust.List() }

// RemoveTrust revokes a trust decision.
func (m *Manager) RemoveTrust(id string) error { return m.trust.Remove(id) }

// buildAdapterConfig resolves a profile plus vault secrets into a protocol
// config. Secrets live only in the returned struct and are cleared by the
// caller after Connect.
func (m *Manager) buildAdapterConfig(c *config.Connection) (protocol.Config, error) {
	pc := protocol.Config{
		Protocol:     c.Protocol,
		Host:         c.Host,
		Port:         c.Port,
		Username:     c.Username,
		Auth:         c.Auth,
		StartDir:     c.StartDir,
		FTPSImplicit: c.Settings.FTPSImplicit,
		TLSVerify:    c.Settings.TLSVerify,
		PassiveMode:  c.Settings.PassiveMode,
		Encoding:     c.Settings.Encoding,
		KeepAlive:    time.Duration(c.Settings.KeepAliveSeconds) * time.Second,
		Trust:        m.trust,
	}
	cred, err := m.creds.Get(context.Background(), c.ID)
	if err != nil && !errors.Is(err, credentials.ErrNotFound) {
		return pc, err
	}
	pc.Password = cred.Password
	pc.PrivateKeyPEM = []byte(cred.PrivateKeyPEM)
	pc.KeyPassphrase = []byte(cred.KeyPassphrase)
	if c.Protocol == config.ProtoFTPS {
		// If a pin exists for this host:port, use it.
		hostport := fmt.Sprintf("%s:%d", c.Host, c.Port)
		for _, e := range m.trust.List() {
			if e.ID == hostport && e.Kind == trust.KindTLSCert {
				pc.TLSPinnedFingerprint = e.Fingerprint
			}
		}
	}
	return pc, nil
}

// Connect opens a session for a profile. A returned trust.Err-like error
// (UnknownHostKeyError / CertVerificationError) signals that the caller must
// obtain an explicit trust decision and call Connect again; the partial
// session is removed.
func (m *Manager) Connect(ctx context.Context, connID string) (*Session, error) {
	c, ok := m.cfg.Connection(connID)
	if !ok {
		return nil, fmt.Errorf("connection %q not found", connID)
	}
	pc, err := m.buildAdapterConfig(c)
	if err != nil {
		return nil, err
	}
	client, err := protocol.Open(pc)
	if err != nil {
		return nil, err
	}

	sess := &Session{
		ID:       uuid.NewString(),
		ConnID:   c.ID,
		ConnName: c.Name,
		Protocol: c.Protocol,
		Host:     c.Host,
		State:    StateConnecting,
		client:   client,
	}
	m.mu.Lock()
	m.sessions[sess.ID] = sess
	m.mu.Unlock()
	m.bus.Emit(events.Event{Type: events.TypeConnectionState, SessionID: sess.ID, ConnID: connID,
		Data: map[string]any{"state": StateConnecting, "name": c.Name}})

	if err := client.Connect(ctx); err != nil {
		m.mu.Lock()
		delete(m.sessions, sess.ID)
		m.mu.Unlock()
		if u, ok := protocol.AsUnknownHostKey(err); ok {
			m.bus.Emit(events.Event{Type: events.TypeHostKeyPrompt, ConnID: connID,
				Data: map[string]any{"sessionId": sess.ID, "retryConnectionId": connID, "kind": "ssh", "hostPort": u.HostPort, "fingerprint": u.Fingerprint, "keyType": u.KeyType}})
			return nil, u
		}
		if ce, ok := protocol.AsCertError(err); ok {
			m.bus.Emit(events.Event{Type: events.TypeHostKeyPrompt, ConnID: connID,
				Data: map[string]any{"sessionId": sess.ID, "retryConnectionId": connID, "kind": "tls", "hostPort": ce.HostPort, "fingerprint": ce.Fingerprint, "subject": ce.Subject, "detail": ce.Detail}})
			return nil, ce
		}
		var ae *protocol.AuthError
		if errors.As(err, &ae) {
			m.bus.Emit(events.Event{Type: events.TypeAuthFailed, ConnID: connID,
				Message: "authentication failed for " + c.Name})
		}
		return nil, err
	}

	sess.State = StateConnected
	sess.Caps = client.Capabilities()
	sess.Server = client.ServerInfo()
	sess.StartDir = client.ServerInfo().StartDir
	sess.CWD = client.ServerInfo().StartDir
	sess.ConnectedAt = time.Now().UTC()
	m.bus.Emit(events.Event{Type: events.TypeConnectionState, SessionID: sess.ID, ConnID: connID,
		Message: "connected to " + c.Name,
		Data:    map[string]any{"state": StateConnected, "name": c.Name}})
	return sess, nil
}

// TestConnection performs a connect + immediate disconnect without creating a
// persistent session.
func (m *Manager) TestConnection(ctx context.Context, connID string) error {
	sess, err := m.Connect(ctx, connID)
	if err != nil {
		return err
	}
	return m.Disconnect(sess.ID)
}

// Disconnect closes a session.
func (m *Manager) Disconnect(sessionID string) error {
	m.mu.Lock()
	s, ok := m.sessions[sessionID]
	if ok {
		delete(m.sessions, sessionID)
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("session %q not found", sessionID)
	}
	err := s.client.Close()
	s.State = StateDisconnected
	m.bus.Emit(events.Event{Type: events.TypeConnectionState, SessionID: sessionID, ConnID: s.ConnID,
		Message: "disconnected from " + s.ConnName,
		Data:    map[string]any{"state": StateDisconnected, "name": s.ConnName}})
	return err
}

// Reconnect closes and reopens a session for the same profile.
func (m *Manager) Reconnect(ctx context.Context, sessionID string) (*Session, error) {
	m.mu.RLock()
	s, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("session %q not found", sessionID)
	}
	connID := s.ConnID
	_ = m.Disconnect(sessionID)
	return m.Connect(ctx, connID)
}

// Sessions returns snapshots of all live sessions.
func (m *Manager) Sessions() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		cp := *s
		cp.client = nil
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ConnectedAt.Before(out[j].ConnectedAt) })
	return out
}

func (m *Manager) session(id string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %q not found (it may have disconnected)", id)
	}
	if s.State != StateConnected {
		return nil, fmt.Errorf("session %q is not connected", id)
	}
	return s, nil
}

// Sessions0 returns a snapshot of one session by ID for read-only metadata.
func (m *Manager) Sessions0(id string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %q not found", id)
	}
	cp := *s
	cp.client = nil
	return &cp, nil
}

// Client returns the underlying protocol client for a session (used by the
// transfer manager).
func (m *Manager) Client(sessionID string) (protocol.Client, error) {
	s, err := m.session(sessionID)
	if err != nil {
		return nil, err
	}
	return s.client, nil
}

// SetCWD records the current directory for a session.
func (m *Manager) SetCWD(sessionID, dir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[sessionID]; ok {
		s.CWD = dir
	}
}

// ---- file operations (thin, audited wrappers) ---------------------------

func (m *Manager) List(ctx context.Context, sessionID, dir string) ([]protocol.Entry, error) {
	cl, err := m.Client(sessionID)
	if err != nil {
		return nil, err
	}
	entries, err := cl.List(ctx, dir)
	if err != nil {
		return nil, err
	}
	m.bus.Emit(events.Event{Type: events.TypeDirectoryListed, SessionID: sessionID,
		Data: map[string]any{"path": dir, "count": len(entries)}})
	return entries, nil
}

func (m *Manager) Mkdir(ctx context.Context, sessionID, p string) error {
	cl, err := m.Client(sessionID)
	if err != nil {
		return err
	}
	if err := cl.Mkdir(ctx, p); err != nil {
		return err
	}
	m.bus.Info(events.TypeInfo, "created directory "+p, events.P("session", sessionID))
	return nil
}

func (m *Manager) Remove(ctx context.Context, sessionID, p string, recursive bool) error {
	cl, err := m.Client(sessionID)
	if err != nil {
		return err
	}
	if err := cl.Remove(ctx, p, recursive); err != nil {
		return err
	}
	m.bus.Info(events.TypeInfo, "deleted "+p, events.P("session", sessionID))
	return nil
}

func (m *Manager) Rename(ctx context.Context, sessionID, from, to string) error {
	cl, err := m.Client(sessionID)
	if err != nil {
		return err
	}
	if err := cl.Rename(ctx, from, to); err != nil {
		return err
	}
	m.bus.Info(events.TypeInfo, "renamed to "+to, events.P("session", sessionID))
	return nil
}

func (m *Manager) Chmod(ctx context.Context, sessionID, p string, mode os.FileMode) error {
	cl, err := m.Client(sessionID)
	if err != nil {
		return err
	}
	if err := cl.Chmod(ctx, p, mode); err != nil {
		return err
	}
	m.bus.Info(events.TypeInfo, fmt.Sprintf("changed permissions of %s to %s", p, mode.String()), events.P("session", sessionID))
	return nil
}

// Shutdown disconnects all sessions cleanly.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.sessions = map[string]*Session{}
	m.mu.Unlock()
	for _, s := range sessions {
		_ = s.client.Close()
	}
}

// TestSession carries the fields needed to inject a live session in tests.
type TestSession struct {
	ID          string
	ConnID      string
	ConnName    string
	Protocol    config.Protocol
	Host        string
	StartDir    string
	CWD         string
	Client      protocol.Client
	ConnectedAt time.Time
}

// InjectTestSession installs a session backed by the given client. Test
// support only.
func (m *Manager) InjectTestSession(id string, ts TestSession) {
	if ts.Protocol == "" {
		ts.Protocol = "sftp"
	}
	if ts.ConnectedAt.IsZero() {
		ts.ConnectedAt = time.Now()
	}
	s := &Session{
		ID:          id,
		ConnID:      ts.ConnID,
		ConnName:    ts.ConnName,
		Protocol:    ts.Protocol,
		Host:        ts.Host,
		State:       StateConnected,
		StartDir:    ts.StartDir,
		CWD:         ts.CWD,
		ConnectedAt: ts.ConnectedAt,
		Caps:        ts.Client.Capabilities(),
		Server: protocol.ServerInfo{
			Protocol:  ts.Protocol,
			Host:      ts.Host,
			StartDir:  ts.StartDir,
			Encrypted: ts.Client.Capabilities().Encrypted,
		},
		client: ts.Client,
	}
	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()
}
