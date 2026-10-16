package ipc

import (
	"errors"

	"golang.org/x/sys/windows"
)

// dialFailure names why a dial failed, for the log only: whatever it is,
// the daemon is started. Windows refuses alike whether the socket file is
// missing or stale, and syscall.ECONNREFUSED does not match its code.
func dialFailure(err error) string {
	if errors.Is(err, windows.WSAECONNREFUSED) {
		return "refused"
	}
	return "other"
}
