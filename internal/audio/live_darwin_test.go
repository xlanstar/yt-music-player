//go:build audiolive

package audio

import (
	"os"
	"runtime"
	"testing"
	"yt-music-player/internal/leakcheck"

	"github.com/ebitengine/purego"
)

// The media controls need the main thread running its run loop, as in a
// MyGo app: the test's main goroutine stays on it and runs it.
func init() { runtime.LockOSThread() }

func runTests(m *testing.M) {
	const path = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
	coreFoundation, err := purego.Dlopen(path, purego.RTLD_LAZY)
	if err != nil {
		panic(err)
	}
	var run func()
	purego.RegisterLibFunc(&run, coreFoundation, "CFRunLoopRun")
	go func() { os.Exit(leakcheck.Run(m)) }()
	run()
}
