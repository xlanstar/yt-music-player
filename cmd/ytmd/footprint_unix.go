//go:build footprint && unix

package main

import (
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
)

// SIGUSR1 frees the heap unconditionally: scripts/footprint.sh's phases
// then all follow the first GCs. Not memprof, whose pprof adds ~2 MB.
func init() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGUSR1)
	go func() {
		for range signals {
			debug.FreeOSMemory()
		}
	}()
}
