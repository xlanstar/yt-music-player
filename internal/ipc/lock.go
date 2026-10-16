package ipc

import (
	"errors"
	"fmt"
	"os"
)

// ErrLocked is returned by TryLock while another process holds the lock:
// a daemon is running already.
var ErrLocked = errors.New("ipc: another ytmd holds the lock")

// Lock is the single-instance lock, held until Unlock or the process ends:
// the OS releases it when its file closes, so a crash leaves none behind.
type Lock struct{ f *os.File }

// TryLock takes the lock at path (Paths.Lock), never waiting: ErrLocked
// when another process holds it. Its directory must exist (Paths.Prepare).
func TryLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // Paths.Lock, not input
	if err != nil {
		return nil, fmt.Errorf("ipc: %w", err)
	}
	if err := tryLock(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Lock{f: f}, nil
}

// Unlock releases the lock. The file stays: removing it would let a
// process lock a new file while another still holds the old one.
func (l *Lock) Unlock() error {
	if err := l.f.Close(); err != nil {
		return fmt.Errorf("ipc: %w", err)
	}
	return nil
}
