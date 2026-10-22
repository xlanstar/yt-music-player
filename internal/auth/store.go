package auth

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sync"
	"yt-music-player/internal/atomicfile"
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
	err := atomicfile.Write(f.path, atomicfile.Options{Sync: true}, func(w io.Writer) error {
		_, err := io.WriteString(w, c.Secret())
		return err
	})
	if err != nil {
		return fmt.Errorf("auth: save: %w", err)
	}
	return nil
}

func (f *fileStore) Clear() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	// With a crash's temporary files, which hold credentials too.
	if err := atomicfile.Remove(f.path); err != nil {
		return fmt.Errorf("auth: clear: %w", err)
	}
	return nil
}
