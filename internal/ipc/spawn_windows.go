package ipc

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// detached gives the daemon no console and a process group of its own, so
// that the console's Ctrl+C and closing it leave the daemon alone.
func detached() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS}
}
