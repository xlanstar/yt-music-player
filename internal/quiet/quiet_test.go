package quiet

import (
	"runtime"
	"runtime/metrics"
	"testing"
	"testing/synctest"
	"time"
	"weak"
)

var sinkBytes []byte

func TestFree(t *testing.T) {
	var g GC
	// Never started: nothing to reset.
	g.Note()
	sinkBytes = make([]byte, 2*minAllocated)
	g.free()
	first := g.last.Load()
	if first == 0 {
		t.Fatal("did not free after 2 MB")
	}
	// A quiet stretch that allocated next to nothing (a song playing
	// unfocused) frees nothing.
	g.free()
	if g.last.Load() != first {
		t.Fatal("freed with nothing allocated")
	}
	sinkBytes = make([]byte, 2*minAllocated)
	g.free()
	if g.last.Load() == first {
		t.Fatal("did not free after 2 MB more")
	}
}

// forced counts the GCs runtime.GC and debug.FreeOSMemory ran.
func forced() uint64 {
	s := [1]metrics.Sample{{Name: "/gc/cycles/forced:gc-cycles"}}
	metrics.Read(s[:])
	return s[0].Value.Uint64()
}

// wantForced fails unless the GCs forced since from are n, once the
// bubble's timers that are due ran.
func wantForced(t *testing.T, from, n uint64, after string) {
	t.Helper()
	synctest.Wait()
	if got := forced() - from; got != n {
		t.Errorf("%s: %d GCs forced, want %d", after, got, n)
	}
}

func TestDropped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := new(GC)
		g.Start()
		defer g.Stop()
		// The first free settles what the test binary allocated so far.
		time.Sleep(after)
		synctest.Wait()

		from := forced()
		sinkBytes = make([]byte, minAllocated/2)
		g.Dropped()
		time.Sleep(after - time.Millisecond)
		wantForced(t, from, 0, "before a quiet stretch")
		time.Sleep(time.Millisecond)
		// A GC for the cleanups the first queues, then the free's own.
		wantForced(t, from, 2, "a quiet stretch after Dropped")

		// One free per Dropped, and no timer runs on.
		time.Sleep(10 * after)
		wantForced(t, from, 2, "a while later")
	})
}

func TestDroppedStoppedFreesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		from := forced()
		var zero GC
		zero.Dropped()
		time.Sleep(2 * after)
		wantForced(t, from, 0, "the zero GC dropped")

		g := new(GC)
		g.Start()
		g.Stop()
		g.Dropped()
		time.Sleep(2 * after)
		wantForced(t, from, 0, "stopped, then dropped")
	})
}

// samples is what a player's cleanup keeps, as MyGo's keeps its voice.
type samples struct{ buf [1 << 16]float32 }

// dropPlayer drops what a player's cleanup holds, as MyGo's, and the
// player; the weak pointer tells when the samples go.
func dropPlayer() weak.Pointer[samples] {
	v := new(samples)
	// Not tiny: tiny allocations share blocks, whose cleanups may never run.
	p := new([64]byte)
	runtime.AddCleanup(p, func(v *samples) { v.buf[0] = 1 }, v)
	return weak.Make(v)
}

// A free after Dropped collects what a dropped object's cleanup held,
// which the GC finding it gone only queues.
func TestDroppedFreesWhatACleanupHeld(t *testing.T) {
	for range 20 {
		held := dropPlayer()
		var g GC
		g.Dropped()
		g.free()
		if held.Value() != nil {
			t.Fatal("a free after Dropped left what a cleanup held")
		}
	}
}
