//go:build audiolive && darwin

package daemon

import (
	"os"
	"runtime"
	"testing"
	"yt-music-player/internal/leakcheck"
)

// The live engine's Now Playing needs the main thread's run loop, as in
// ytmd.
func init() { runtime.LockOSThread() }

// runTests runs the tests off the main thread, which runs the run loop.
func runTests(m *testing.M) {
	go func() { os.Exit(leakcheck.Run(m)) }()
	RunMain(func() {})
}
