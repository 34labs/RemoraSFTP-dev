// Package apppaths locates and manages the per-user RemoraSFTP data directory.
//
// All non-secret application state (connection profiles, settings, trusted
// host keys, activity log) lives under a single OS-appropriate directory.
// Secrets are handled separately by the credentials package (OS keychain when
// available, encrypted file fallback inside this directory).
package apppaths

import (
	"errors"
	"os"
	"path/filepath"
)

const appName = "RemoraSFTP"

// dir is resolved once and cached.
var dir string

// Dir returns (creating if necessary) the application data directory.
//
// Resolution order:
//  1. SFTPBOX_DATA_DIR environment variable (explicit override, testing/dev)
//  2. Portable mode: a "data" directory next to the executable
//  3. OS user config directory: %AppData%\RemoraSFTP (Windows),
//     ~/Library/Application Support/RemoraSFTP (macOS),
//     $XDG_CONFIG_HOME/remorasftp or ~/.config/remorasftp (Linux)
func Dir() (string, error) {
	if dir != "" {
		return dir, nil
	}
	d, err := resolve()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	dir = d
	return d, nil
}

// SetDir overrides the resolved directory. Mainly used by tests.
func SetDir(d string) { dir = d }

func resolve() (string, error) {
	if env := os.Getenv("SFTPBOX_DATA_DIR"); env != "" {
		return filepath.Clean(env), nil
	}
	if exe, err := os.Executable(); err == nil {
		portable := filepath.Join(filepath.Dir(exe), "data")
		if info, err := os.Stat(portable); err == nil && info.IsDir() {
			return portable, nil
		}
	}
	base, err := os.UserConfigDir()
	if err != nil {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", errors.New("cannot locate a user config directory: " + err.Error())
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, appName), nil
}

// File returns the absolute path of a file inside the data directory.
func File(name ...string) (string, error) {
	base, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{base}, name...)...), nil
}

// Ensure creates a subdirectory inside the data directory with 0700 perms.
func Ensure(names ...string) (string, error) {
	p, err := File(names...)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(p, 0o700); err != nil {
		return "", err
	}
	return p, nil
}

// TempDir returns (and creates) the isolated temporary transfer directory.
// Callers create per-transfer subdirectories inside it. The whole directory is
// wiped on clean startup and on graceful shutdown.
func TempDir() (string, error) {
	return Ensure("tmp")
}

// WipeTemp removes all temporary transfer artifacts. It is safe to call when
// the directory does not exist.
func WipeTemp() error {
	p, err := File("tmp")
	if err != nil {
		return err
	}
	return os.RemoveAll(p)
}
