package credentials

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/zalando/go-keyring"
)

// keychainProvider stores one JSON blob per connection in the OS-native
// secret store via go-keyring.
type keychainProvider struct{}

func newKeychainProvider() (*keychainProvider, error) {
	// Probe the backend. On Linux without a Secret Service/D-Bus this
	// returns an error; callers fall back to the file provider.
	if err := keyring.Set(ServiceName, "__sftpbox_probe__", "probe"); err != nil {
		return nil, err
	}
	_ = keyring.Delete(ServiceName, "__sftpbox_probe__")
	return &keychainProvider{}, nil
}

func (p *keychainProvider) Name() string {
	return "OS keychain (Credential Manager / Keychain / Secret Service)"
}
func (p *keychainProvider) Available() bool { return true }

func accountName(connID string) string { return "remorasftp-conn-" + connID }

func (p *keychainProvider) Put(_ context.Context, c Credential) error {
	if c.ConnectionID == "" {
		return errors.New("connection id required")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return keyring.Set(ServiceName, accountName(c.ConnectionID), string(raw))
}

func (p *keychainProvider) Get(_ context.Context, connID string) (Credential, error) {
	raw, err := keyring.Get(ServiceName, accountName(connID))
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return Credential{}, ErrNotFound
		}
		return Credential{}, err
	}
	var c Credential
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return Credential{}, err
	}
	return c, nil
}

func (p *keychainProvider) Delete(_ context.Context, connID string) error {
	err := keyring.Delete(ServiceName, accountName(connID))
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return err
	}
	return nil
}
