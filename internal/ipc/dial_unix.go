//go:build unix

package ipc

import (
	"errors"
	"syscall"
)

// dialFailure names why a dial failed, for the log only: whatever it is,
// the daemon is started.
func dialFailure(err error) string {
	switch {
	case errors.Is(err, syscall.ENOENT):
		return "no socket"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "stale socket"
	}
	return "other"
}
