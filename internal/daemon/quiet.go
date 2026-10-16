package daemon

import (
	"runtime/debug"
	"runtime/metrics"
	"sync/atomic"
	"time"
)

const (
	// quietAfter is how long the loop must have run nothing before the
	// heap is given back.
	quietAfter = 3 * time.Second
	// quietMin is the bytes the heap must allocate between frees to make
	// a full GC worth it: a page of a catalog request is tens of KB, a
	// snapshot of a long queue about a MB.
	quietMin = 1 << 20
)

// quietGC returns free heap memory to the OS once the loop has run nothing
// for quietAfter, as Desktop's does once its view stops building: busy,
// the heap grows to twice the live size, and the scavenger takes minutes
// to give it back. Activity arms its one timer; idle, nothing wakes.
type quietGC struct {
	timer *time.Timer // the loop's
	// last is the heap's total allocations at the last free; atomic as a
	// free runs on the timer's goroutine.
	last atomic.Uint64
}

func (q *quietGC) active() {
	if q.timer == nil {
		q.timer = time.AfterFunc(quietAfter, q.free)
		return
	}
	q.timer.Reset(quietAfter)
}

func (q *quietGC) stop() {
	if q.timer != nil {
		q.timer.Stop()
	}
}

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
