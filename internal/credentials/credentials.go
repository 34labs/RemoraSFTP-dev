// Package credentials stores sensitive connection material (passwords,
// private keys, key passphrases) behind a provider abstraction.
//
// Resolution order at runtime:
//  1. Native OS keychain via go-keyring (Windows Credential Manager,
//     macOS Keychain, Linux Secret Service/D-Bus).
//  2. Encrypted local file fallback (AES-256-GCM, key stored in a 0600 file
//     inside the application data directory). This is device-local storage
//     used when no keychain service is available (e.g. headless Linux).
//
// Secrets are never written to config.json, logs, URLs, the DOM, or analytics.
// The API never returns secrets to the browser; the UI can only test for the
// presence of a stored secret or replace/delete it.
package credentials

import (
	"context"
	"errors"
)

// ServiceName is the keychain service label used across platforms.
const ServiceName = "RemoraSFTP"

// ErrNotFound is returned when no credential exists for a connection.
var ErrNotFound = errors.New("credential not found")

// Credential holds all secret material for one connection. Empty fields mean
// "no value". Callers should zero the struct as soon as it is no longer needed.
type Credential struct {
	ConnectionID  string `json:"connectionId"`
	Password      string `json:"password,omitempty"`
	PrivateKeyPEM string `json:"privateKeyPem,omitempty"`
	KeyPassphrase string `json:"keyPassphrase,omitempty"`
}

// HasAnySecret reports whether any secret material is present.
func (c Credential) HasAnySecret() bool {
	return c.Password != "" || c.PrivateKeyPEM != "" || c.KeyPassphrase != ""
}

// Provider abstracts a secret storage backend.
type Provider interface {
	// Name returns a human-readable backend name ("Keychain", "Encrypted file").
	Name() string
	// Available reports whether the backend can be used on this system.
	Available() bool
	// Put stores (creating or replacing) the credential.
	Put(ctx context.Context, c Credential) error
	// Get retrieves the credential for a connection.
	Get(ctx context.Context, connectionID string) (Credential, error)
	// Delete removes any stored credential for a connection. It is not an
	// error if nothing is stored.
	Delete(ctx context.Context, connectionID string) error
}

// New selects the best available provider: native keychain when present,
// otherwise the encrypted local-file fallback.
func New(dataDir string) (Provider, error) {
	if kp, err := newKeychainProvider(); err == nil && kp.Available() {
		return kp, nil
	} else if err != nil {
		// Fall through to file backend; keychain absence is expected on
		// headless systems and is not fatal.
		_ = err
	}
	return newFileProvider(dataDir)
}

// MustAvailable forces a specific backend, used by tests and the "storage"
// diagnostics UI. It returns the file provider directly when keychain is
// unavailable.
func MustAvailable(dataDir string) (Provider, error) {
	return newFileProvider(dataDir)
}
