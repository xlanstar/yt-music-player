//go:build unix

package remote

import (
	"errors"
	"syscall"
)

// processAlive reports whether process pid runs (or is a zombie not yet
// reaped).
func processAlive(pid int) bool {
	if pid <= 0 { // 0 and below name process groups
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// killProcess ends process pid at once.
func killProcess(pid int) error {
	if pid <= 0 {
		return syscall.ESRCH
	}
	return syscall.Kill(pid, syscall.SIGKILL)
}
