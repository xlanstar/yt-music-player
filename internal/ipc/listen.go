package ipc

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
)

// Listen listens on the socket at path (Paths.Socket), which only its
// user may connect to. It first removes a socket left there by a daemon
// that crashed, so take the lock first (TryLock): the socket is then no
// live daemon's. Closing the listener removes the socket.
func Listen(path string) (*net.UnixListener, error) {
	if err := removeStale(path); err != nil {
		return nil, err
	}
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("ipc: %w", err)
	}
	if err := privateSocket(path); err != nil {
		_ = l.Close()
		return nil, fmt.Errorf("ipc: %w", err)
	}
	return l, nil
}

// removeStale removes the socket at path; anything else there stays, for
// Listen to fail on.
func removeStale(path string) error {
	info, err := os.Lstat(path) //nolint:gosec // Paths.Socket, not input
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("ipc: %w", err)
	}
	if info.Mode().Type() != fs.ModeSocket {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) { //nolint:gosec // Paths.Socket
		return fmt.Errorf("ipc: %w", err)
	}
	return nil
}

// Dial connects to the socket at path once, never starting ytmd.
func Dial(ctx context.Context, path string) (net.Conn, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("ipc: %w", err)
	}
	return c, nil
}
