// Package trust persists explicit user trust decisions about server identity:
// SSH host keys for SFTP and pinned TLS certificates for FTPS.
//
// RemoraSFTP never accepts an unknown host key or certificate silently. On first
// encounter the UI presents the fingerprint and requires an explicit trust
// decision; that decision is recorded here and checked on every subsequent
// connection. Entries are device-local, non-secret metadata.
package trust

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// EntryKind distinguishes SSH host keys from TLS certificate pins.
type EntryKind string

const (
	KindSSHKey  EntryKind = "ssh-host-key"
	KindTLSCert EntryKind = "tls-certificate"
)

// Entry is one trusted server identity.
type Entry struct {
	ID          string    `json:"id"` // host:port
	Kind        EntryKind `json:"kind"`
	Fingerprint string    `json:"fingerprint"` // display form, e.g. "SHA256:..."
	Algorithm   string    `json:"algorithm,omitempty"`
	Note        string    `json:"note,omitempty"`
	TrustedAt   time.Time `json:"trustedAt"`
}

// Store is the JSON-backed trust store.
type Store struct {
	mu      sync.RWMutex
	path    string
	entries map[string]*Entry
	// pending captures the most recently presented, untrusted identity per
	// host:port so an explicit user confirmation can persist it.
	pending map[string]pendingIdentity
}

type pendingIdentity struct {
	kind        EntryKind
	fingerprint string
	sshKey      ssh.PublicKey
	cert        *x509.Certificate
}

// Open loads (or creates) the trust store at <dataDir>/hostkeys.json.
func Open(dataDir string) (*Store, error) {
	path := filepath.Join(dataDir, "hostkeys.json")
	s := &Store{path: path, entries: map[string]*Entry{}, pending: map[string]pendingIdentity{}}
	raw, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		return s, nil
	case err != nil:
		return nil, err
	}
	var doc struct {
		Entries []*Entry `json:"entries"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("trust store %s is corrupt: %w", path, err)
	}
	for _, e := range doc.Entries {
		s.entries[e.ID] = e
	}
	return s, nil
}

func (s *Store) save() error {
	list := make([]*Entry, 0, len(s.entries))
	for _, e := range s.entries {
		list = append(list, e)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	raw, err := json.MarshalIndent(struct {
		Entries []*Entry `json:"entries"`
	}{Entries: list}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// SSHFingerprint returns the OpenSSH-style SHA256 fingerprint of a host key.
func SSHFingerprint(key ssh.PublicKey) string {
	return ssh.FingerprintSHA256(key)
}

// CertFingerprint returns a hex SHA256 fingerprint of a TLS leaf certificate's
// DER encoding, used for pinning.
func CertFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return "SHA256:" + hex.EncodeToString(sum[:])
}

// CheckSSH reports whether hostport has a trusted key matching the presented
// key. Returns the presented fingerprint for UI display. An untrusted key is
// stashed as pending so TrustPending can persist it after user confirmation.
func (s *Store) CheckSSH(hostport string, key ssh.PublicKey) (trusted bool, fingerprint string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fp := SSHFingerprint(key)
	e, ok := s.entries[hostport]
	trusted = ok && e.Kind == KindSSHKey && e.Fingerprint == fp
	if !trusted {
		s.pending[hostport] = pendingIdentity{kind: KindSSHKey, fingerprint: fp, sshKey: key}
	}
	return trusted, fp
}

// TrustSSH records an explicit trust decision for an SSH host key.
func (s *Store) TrustSSH(hostport string, key ssh.PublicKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[hostport] = &Entry{
		ID:          hostport,
		Kind:        KindSSHKey,
		Fingerprint: SSHFingerprint(key),
		Algorithm:   key.Type(),
		TrustedAt:   time.Now().UTC(),
	}
	return s.save()
}

// CheckCert reports whether hostport has a pinned TLS certificate matching
// the presented leaf certificate. A non-matching certificate is stashed as
// pending so the user can explicitly pin it.
func (s *Store) CheckCert(hostport string, cert *x509.Certificate) (trusted bool, fingerprint string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fp := CertFingerprint(cert)
	e, ok := s.entries[hostport]
	trusted = ok && e.Kind == KindTLSCert && e.Fingerprint == fp
	if !trusted {
		s.pending[hostport] = pendingIdentity{kind: KindTLSCert, fingerprint: fp, cert: cert}
	}
	return trusted, fp
}

// TrustPending persists a pending identity once the user has confirmed its
// fingerprint. Returns an error if no matching pending identity exists.
func (s *Store) TrustPending(hostport, fingerprint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pending[hostport]
	if !ok || p.fingerprint != fingerprint {
		return fmt.Errorf("no pending identity for %s matching that fingerprint", hostport)
	}
	switch p.kind {
	case KindSSHKey:
		if p.sshKey == nil {
			return fmt.Errorf("pending host key expired; reconnect to present it again")
		}
		s.entries[hostport] = &Entry{
			ID:          hostport,
			Kind:        KindSSHKey,
			Fingerprint: p.fingerprint,
			Algorithm:   p.sshKey.Type(),
			TrustedAt:   time.Now().UTC(),
		}
	case KindTLSCert:
		if p.cert == nil {
			return fmt.Errorf("pending certificate expired; reconnect to present it again")
		}
		s.entries[hostport] = &Entry{
			ID:          hostport,
			Kind:        KindTLSCert,
			Fingerprint: p.fingerprint,
			Algorithm:   p.cert.SignatureAlgorithm.String(),
			Note:        p.cert.Subject.String(),
			TrustedAt:   time.Now().UTC(),
		}
	}
	delete(s.pending, hostport)
	return s.save()
}

// TrustCert records an explicit pin for a TLS leaf certificate.
func (s *Store) TrustCert(hostport string, cert *x509.Certificate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[hostport] = &Entry{
		ID:          hostport,
		Kind:        KindTLSCert,
		Fingerprint: CertFingerprint(cert),
		Algorithm:   cert.SignatureAlgorithm.String(),
		Note:        cert.Subject.String(),
		TrustedAt:   time.Now().UTC(),
	}
	return s.save()
}

// List returns a sorted snapshot of all trust entries.
func (s *Store) List() []*Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Entry, 0, len(s.entries))
	for _, e := range s.entries {
		cp := *e
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Remove deletes a trust entry.
func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, id)
	return s.save()
}

// B64SHA256 is a small helper for callers that need a raw fingerprint.
func B64SHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return "SHA256:" + base64.StdEncoding.EncodeToString(sum[:])
}
