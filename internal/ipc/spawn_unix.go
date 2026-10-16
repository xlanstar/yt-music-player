//go:build unix

package ipc

import "syscall"

// detached puts the daemon in a session of its own, so that the
// terminal's signals and closing it leave the daemon alone.
func detached() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
