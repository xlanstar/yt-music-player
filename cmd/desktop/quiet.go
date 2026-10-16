package main

import (
	"runtime/debug"
	"runtime/metrics"
	"sync/atomic"
	"time"
)

const (
	// quietAfter is longer than the 1 s step of a song playing focused,
	// so playback in front never triggers a free.
	quietAfter = 3 * time.Second
	// quietMin is the bytes the heap must allocate between frees to make
	// a full GC worth it: a step of a song playing unfocused allocates a
	// few KB, a thumbnail about a MB.
	quietMin = 1 << 20
)

// quietGC returns free heap memory to the OS once the view has not built
// for quietAfter: busy, the heap grows to twice the live size, and the
// scavenger takes minutes to return it (docs/performance.md). It frees
// off the UI thread; the zero value never frees.
type quietGC struct {
	timer *time.Timer
	// last is the heap's total allocations at the last free; atomic as a
	// free can outlast a wait.
	last atomic.Uint64
}

func (q *quietGC) start() { q.timer = time.AfterFunc(quietAfter, q.free) }

func (q *quietGC) built() {
	if q.timer != nil {
		q.timer.Reset(quietAfter)
	}
}

// free costs 0.6–5 ms.
func (q *quietGC) free() {
	samples := [1]metrics.Sample{{Name: "/gc/heap/allocs:bytes"}}
	metrics.Read(samples[:])
	allocated := samples[0].Value.Uint64()
	if allocated-q.last.Load() < quietMin {
		return
	}
	q.last.Store(allocated)
	debug.FreeOSMemory()
}
