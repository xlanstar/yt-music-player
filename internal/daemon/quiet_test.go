package daemon

import (
	"runtime/metrics"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

// forcedGCs counts the GCs runtime.GC and debug.FreeOSMemory ran.
func forcedGCs() uint64 {
	s := [1]metrics.Sample{{Name: "/gc/cycles/forced:gc-cycles"}}
	metrics.Read(s[:])
	return s[0].Value.Uint64()
}

// A stream released paused frees the heap a quiet stretch later, with a
// GC more for MyGo's cleanup, though the loop allocated next to nothing.
func TestReleaseFreesTheHeap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, Options{Seed: songs(2)})
		h.do(h.svc.quiet.Start)
		h.do(h.svc.player.Resume)
		h.ready()
		h.do(h.svc.player.Pause)
		// The pause's own free, 3 s on, comes and goes.
		time.Sleep(time.Minute)
		synctest.Wait()
		if !slices.Contains(h.calls(), "release") {
			t.Fatal("not released a minute into the pause")
		}
		from := forcedGCs()
		time.Sleep(3*time.Second + time.Millisecond)
		synctest.Wait()
		if got := forcedGCs() - from; got != 2 {
			t.Errorf("%d GCs forced after the release, want 2", got)
		}
		time.Sleep(time.Hour)
		synctest.Wait()
		if got := forcedGCs() - from; got != 2 {
			t.Errorf("%d GCs forced an hour paused, want 2", got)
		}
	})
}
