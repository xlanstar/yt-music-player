// Package quiet gives free heap memory back to the OS once a process has
// gone quiet: busy, the heap grows to twice the live size, and the
// scavenger takes minutes to return it (docs/performance.md). The Desktop
// is quiet once it stops building, the TUI once Update rests, ytmd once its
// loop stops running; ytmd also frees what a released stream held.
package quiet

import (
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"sync/atomic"
	"time"
)

const (
	// after is longer than the 1 s step of a song playing focused, so
	// playback in front never triggers a free.
	after = 3 * time.Second
	// minAllocated is the bytes the heap must allocate between frees to
	// make a full GC worth it: a step of a song playing unfocused
	// allocates a few KB, a thumbnail or a long queue's snapshot about a MB.
	minAllocated = 1 << 20
	// droppedBit marks last for a free whatever the heap allocated; the
	// heap's allocations never reach it.
	droppedBit = 1 << 63
	// maxYields bounds the wait for cleanups (~1 µs a yield) by count, not
	// time: a test's synctest bubble stops its clock while goroutines run.
	maxYields = 4096
)

// GC frees on its timer's goroutine, after a quiet stretch; activity only
// resets the one timer, so an idle process wakes for nothing. The zero
// value never frees, as tests' apps and benchmarks' services.
type GC struct {
	timer *time.Timer
	// last is the heap's total allocations at the last free, droppedBit
	// set by Dropped since; atomic as a free can outlast a wait.
	last atomic.Uint64
}

// Start frees after a quiet stretch from now on.
func (g *GC) Start() { g.timer = time.AfterFunc(after, g.free) }

// Note restarts the quiet stretch.
func (g *GC) Note() {
	if g.timer != nil {
		g.timer.Reset(after)
	}
}

// Dropped has the next free, a quiet stretch from now, run however little
// the heap allocated: memory let go of, as a released stream's, is garbage.
func (g *GC) Dropped() {
	g.last.Or(droppedBit)
	g.Note()
}

// Stop ends the frees, also those a later Note or Dropped would start.
func (g *GC) Stop() {
	if g.timer != nil {
		g.timer.Stop()
		g.timer = nil
	}
}

// free costs 0.6–5 ms.
func (g *GC) free() {
	samples := [1]metrics.Sample{{Name: "/gc/heap/allocs:bytes"}}
	metrics.Read(samples[:])
	allocated := samples[0].Value.Uint64()
	last := g.last.Load()
	dropped := last&droppedBit != 0
	if !dropped && allocated-last < minAllocated {
		return
	}
	if !g.last.CompareAndSwap(last, allocated) {
		// Dropped meanwhile: the next free collects twice.
		g.last.Store(allocated | droppedBit)
	}
	if dropped {
		// MyGo's cleanup of a dropped player keeps its decoder and second
		// of samples until it ran: this GC queues it, FreeOSMemory's frees.
		runtime.GC()
		awaitCleanups()
	}
	debug.FreeOSMemory()
}

// awaitCleanups yields until the cleanups queued so far ran, a few
// thousand yields at most: the next GC would otherwise often start first.
func awaitCleanups() {
	s := [2]metrics.Sample{
		{Name: "/gc/cleanups/queued:cleanups"},
		{Name: "/gc/cleanups/executed:cleanups"},
	}
	for range maxYields {
		metrics.Read(s[:])
		if s[0].Value.Kind() != metrics.KindUint64 || s[1].Value.Uint64() >= s[0].Value.Uint64() {
			return
		}
		runtime.Gosched()
	}
}
