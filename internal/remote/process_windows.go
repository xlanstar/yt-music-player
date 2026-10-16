package remote

import (
	"math"

	"golang.org/x/sys/windows"
)

// processAlive reports whether process pid runs.
func processAlive(pid int) bool {
	if pid <= 0 || pid > math.MaxUint32 {
		return false
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h) //nolint:errcheck // a handle only read
	event, err := windows.WaitForSingleObject(h, 0)
	return err == nil && event == uint32(windows.WAIT_TIMEOUT)
}

// killProcess ends process pid at once.
func killProcess(pid int) error {
	if pid <= 0 || pid > math.MaxUint32 {
		return windows.ERROR_INVALID_PARAMETER
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h) //nolint:errcheck // closing after the fact
	return windows.TerminateProcess(h, 1)
}
