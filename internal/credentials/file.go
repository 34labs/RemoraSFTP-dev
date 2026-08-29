package credentials

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// fileProvider is the device-local fallback when no OS keychain is available.
//
// Layout inside the data directory:
//
//	secrets/master.key   32 random bytes, mode 0600
//	secrets/<conn>.enc   nonce || AES-256-GCM(ciphertext), mode 0600
//
// The master key never leaves the device and is never logged or transmitted.
// This is weaker than OS keychain integration (which can leverage hardware
// -backed storage and user-login binding) but keeps secrets encrypted at rest
// with correct permissions instead of plaintext on disk.
type fileProvider struct {
	dir string
	key []byte
}

func newFileProvider(dataDir string) (*fileProvider, error) {
	dir := filepath.Join(dataDir, "secrets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	keyPath := filepath.Join(dir, "master.key")
	key, err := os.ReadFile(keyPath)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, key); err != nil {
			return nil, err
		}
		if err := os.WriteFile(keyPath, key, 0o600); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("master key in %s is malformed; refusing to start (delete it only if you intend to re-enter credentials)", keyPath)
	}
	return &fileProvider{dir: dir, key: key}, nil
}

func (p *fileProvider) Name() string {
	return "Encrypted local file (device-local fallback)"
}

func (p *fileProvider) Available() bool { return true }

func (p *fileProvider) path(connID string) string {
	// connID is a UUID; sanitize defensively.
	safe := ""
	for _, r := range connID {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			safe += string(r)
		}
	}
	if safe == "" {
		safe = "unknown"
	}
	return filepath.Join(p.dir, safe+".enc")
}

func (p *fileProvider) seal(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(p.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func (p *fileProvider) open(blob []byte) ([]byte, error) {
	block, err := aes.NewCipher(p.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(blob) < ns+1 {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ct := blob[:ns], blob[ns:]
	return gcm.Open(nil, nonce, ct, nil)
}

func (p *fileProvider) Put(_ context.Context, c Credential) error {
	if c.ConnectionID == "" {
		return errors.New("connection id required")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	blob, err := p.seal(raw)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(p.path(c.ConnectionID), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(blob); err != nil {
		return err
	}
	runtime.KeepAlive(raw)
	return f.Sync()
}

func (p *fileProvider) Get(_ context.Context, connID string) (Credential, error) {
	blob, err := os.ReadFile(p.path(connID))
	if errors.Is(err, os.ErrNotExist) {
		return Credential{}, ErrNotFound
	}
	if err != nil {
		return Credential{}, err
	}
	raw, err := p.open(blob)
	if err != nil {
		return Credential{}, err
	}
	var c Credential
	if err := json.Unmarshal(raw, &c); err != nil {
		return Credential{}, err
	}
	return c, nil
}

func (p *fileProvider) Delete(_ context.Context, connID string) error {
	err := os.Remove(p.path(connID))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
