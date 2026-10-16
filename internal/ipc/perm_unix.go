//go:build unix

package ipc

import (
	"fmt"
	"os"
	"syscall"
)

// privateDir makes dir, or takes it when it is the user's own directory,
// and makes it 0700. In a shared directory, a symlink is refused too.
func privateDir(dir string, uid int, shared bool) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("ipc: %w", err)
	}
	stat := os.Stat
	if shared {
		stat = os.Lstat
	}
	info, err := stat(dir)
	if err != nil {
		return fmt.Errorf("ipc: %w", err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	switch {
	case !info.IsDir():
		return fmt.Errorf("ipc: %s is not a directory", dir)
	case !ok || int64(st.Uid) != int64(uid):
		return fmt.Errorf("ipc: %s belongs to another user", dir)
	case info.Mode().Perm() != 0o700:
		if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // a directory needs its x bit
			return fmt.Errorf("ipc: %w", err)
		}
	}
	return nil
}

// privateSocket makes the socket at path the user's alone: it is made as
// the umask says (0755 on macOS).
func privateSocket(path string) error {
	return os.Chmod(path, 0o600) //nolint:gosec // Paths.Socket, not input
}
