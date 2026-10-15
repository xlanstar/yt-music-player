// Package budget fails tests that allocate more than allowed, so a
// regression shows in `go test` rather than in a later profile. Allocation
// counts are deterministic where time is not, so tests cap them, not ns/op.
package budget

import (
	"runtime"
	"testing"
)

// Allocs fails t when f allocates more than limit times per call, averaged
// over runs calls. Set limit a little above today's count so dependency
// noise passes but a real regression does not. Unlike testing.AllocsPerRun,
// which divides as integers, the average keeps its fraction: an allocation
// every few calls (a slice growing) counts.
func Allocs(t *testing.T, limit float64, runs int, f func()) {
	t.Helper()
	if got := measure(t, runs, f).Allocs; got > limit {
		t.Errorf("%.2f allocs per call, budget %.2f", got, limit)
	}
}

// Within fails t as Allocs does, and also when f allocates more than
// limit.Bytes per call. Use it where bytes matter: a decoded image, of
// which a page holds tens, sets the footprint more than its count does.
func Within(t *testing.T, limit Cost, runs int, f func()) {
	t.Helper()
	got := measure(t, runs, f)
	if got.Allocs > limit.Allocs {
		t.Errorf("%.2f allocs per call, budget %.2f", got.Allocs, limit.Allocs)
	}
	if got.Bytes > limit.Bytes {
		t.Errorf("%.0f bytes per call, budget %.0f", got.Bytes, limit.Bytes)
	}
}

// measure is Measure for t, which it skips under the race detector; -short
// runs f at most 10 times.
func measure(t *testing.T, runs int, f func()) Cost {
	t.Helper()
	if Race {
		t.Skip("the race detector allocates on its own")
	}
	if testing.Short() {
		runs = min(runs, 10)
	}
	return Measure(runs, f)
}

// Cost is what a call allocates on average.
type Cost struct{ Allocs, Bytes float64 }

// Measure returns what f allocates per call, averaged over runs calls after
// one warm-up call. Like testing.AllocsPerRun it runs on one P so other
// goroutines hardly interfere, but it also counts bytes.
func Measure(runs int, f func()) Cost {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	// The runtime allocates too when a GC first starts on this many Ps (a
	// mark worker, threads to wake): a GC now keeps that out of the count.
	runtime.GC()
	f()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range runs {
		f()
	}
	runtime.ReadMemStats(&after)
	count := float64(runs)
	return Cost{
		Allocs: float64(after.Mallocs-before.Mallocs) / count,
		Bytes:  float64(after.TotalAlloc-before.TotalAlloc) / count,
	}
}
