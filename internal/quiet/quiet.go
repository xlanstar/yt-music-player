// Package quiet gives free heap memory back to the OS once a process has
// gone quiet: busy, the heap grows to twice the live size, and the
// scavenger takes minutes to return it (docs/performance.md). The Desktop
// is quiet once it stops building, the TUI once Update rests, ytmd once its
// loop stops running.
package quiet

import (
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
)

// GC frees on its timer's goroutine, after a quiet stretch; activity only
// resets the one timer, so an idle process wakes for nothing. The zero
// value never frees, as tests' apps and benchmarks' services.
type GC struct {
	timer *time.Timer
	// last is the heap's total allocations at the last free; atomic as a
	// free can outlast a wait.
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

// Stop ends the frees.
func (g *GC) Stop() {
	if g.timer != nil {
		g.timer.Stop()
	}
}

// free costs 0.6–5 ms.
func (g *GC) free() {
	samples := [1]metrics.Sample{{Name: "/gc/heap/allocs:bytes"}}
	metrics.Read(samples[:])
	allocated := samples[0].Value.Uint64()
	if allocated-g.last.Load() < minAllocated {
		return
	}
	g.last.Store(allocated)
	debug.FreeOSMemory()
}
