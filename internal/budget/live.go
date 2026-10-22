package budget

import (
	"runtime"
	"weak"
)

// LiveHeap returns the bytes reachable once a GC ran: what a test keeps,
// as the heap with it less the heap once it is dropped.
func LiveHeap() int64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int64(m.HeapAlloc) //nolint:gosec // G115: a heap is far below 2^63 bytes
}

// IsCollected runs GCs, up to 10, until p's value is collected, and reports
// whether it was: a test fails on a leak, rather than measuring it.
func IsCollected[T any](p weak.Pointer[T]) bool {
	for range 10 {
		if p.Value() == nil {
			return true
		}
		runtime.GC()
	}
	return p.Value() == nil
}
