package auth

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// ErrNotStored is returned by Store.Load when no credentials are saved.
var ErrNotStored = errors.New("auth: no stored credentials")

// Store keeps credentials across launches. Implementations are safe for
// concurrent use.
type Store interface {
	// Load returns the saved credentials, or ErrNotStored.
	Load() (Credentials, error)
	// Save replaces the saved credentials.
	Save(Credentials) error
	// Clear forgets the saved credentials; clearing none is no error.
	Clear() error
}

// appName names the Keychain service and the config directory.
const appName = "yt-music-player"

// MemoryStore keeps credentials for the life of the process, for tests and
// when the platform store is unavailable.
type MemoryStore struct {
	mu    sync.Mutex
	creds Credentials
}

// Load implements Store.
func (m *MemoryStore) Load() (Credentials, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.creds.IsZero() {
		return Credentials{}, ErrNotStored
	}
	return m.creds, nil
}

// Save implements Store.
func (m *MemoryStore) Save(c Credentials) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.creds = c
	return nil
}

// Clear implements Store.
func (m *MemoryStore) Clear() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.creds = Credentials{}
	return nil
}

// fileStore keeps credentials in a file only the user can read.
type fileStore struct {
	path string
	mu   sync.Mutex
}

func (f *fileStore) Load() (Credentials, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, err := os.ReadFile(f.path)
	if errors.Is(err, fs.ErrNotExist) {
		return Credentials{}, ErrNotStored
	}
	if err != nil {
		return Credentials{}, fmt.Errorf("auth: load: %w", err)
	}
	return Parse(string(data))
}

// Save writes a temporary file and renames it so a crash leaves the old
// credentials or the new, never half.
func (f *fileStore) Save(c Credentials) error {
	if c.IsZero() {
		return f.Clear()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	dir := filepath.Dir(f.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("auth: save: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".credentials-*")
	if err != nil {
		return fmt.Errorf("auth: save: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // Fails harmlessly once renamed.
	err = writeSecret(tmp, c.Secret())
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), f.path)
	}
	if err != nil {
		return fmt.Errorf("auth: save: %w", err)
	}
	return nil
}

// writeSecret writes secret to tmp and syncs it; the caller closes tmp.
func writeSecret(tmp *os.File, secret string) error {
	// CreateTemp already makes it 0600; Chmod guards against an umask or
	// filesystem that did otherwise.
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.WriteString(secret); err != nil {
		return err
	}
	return tmp.Sync()
}

func (f *fileStore) Clear() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := os.Remove(f.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("auth: clear: %w", err)
	}
	return nil
}
