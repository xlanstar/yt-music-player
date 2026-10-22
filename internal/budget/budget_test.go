package budget

import (
	"runtime"
	"runtime/metrics"
	"testing"
	"weak"
)

var sink []byte

func TestBudgetAllocs(t *testing.T) {
	Allocs(t, 1, 100, func() { sink = make([]byte, 64) })
}

func TestBudgetWithin(t *testing.T) {
	Within(t, Cost{Allocs: 1, Bytes: 64}, 100, func() { sink = make([]byte, 64) })
}

func TestBudgetMeasure(t *testing.T) {
	if Race {
		t.Skip("the race detector allocates on its own")
	}
	got := Measure(100, func() { sink = make([]byte, 64) })
	if got.Allocs != 1 || got.Bytes != 64 {
		t.Errorf("measured %+v, want 1 alloc of 64 bytes", got)
	}
}

var garbage [][]byte

// Measure starts with nothing for the scavenger to return, after a test
// that freed much (a synctest one): else, waking on Measure's one P, the
// scavenger grew its timer heap now and then, 0.01 allocs per call.
func TestBudgetMeasureQuietsScavenger(t *testing.T) {
	for range 64 {
		garbage = append(garbage, make([]byte, 1<<20))
	}
	garbage = nil
	free := []metrics.Sample{{Name: "/memory/classes/heap/free:bytes"}}
	isFirst := true
	Measure(1, func() {
		if isFirst {
			metrics.Read(free)
			isFirst = false
		}
	})
	if got := free[0].Value.Uint64(); got > 1<<20 {
		t.Errorf("%d bytes free but not returned when Measure starts", got)
	}
}

func TestLiveHeap(t *testing.T) {
	if Race {
		t.Skip("the race detector allocates on its own")
	}
	sink = make([]byte, 1<<20)
	with := LiveHeap()
	sink = nil
	// Within 100 KB: the runtime and testing allocate meanwhile.
	if freed := with - LiveHeap(); freed < 900<<10 {
		t.Errorf("%d bytes freed, want the MB dropped", freed)
	}
}

func TestIsCollected(t *testing.T) {
	kept := new([64]byte)
	if IsCollected(weak.Make(kept)) {
		t.Error("a value still reachable collected")
	}
	runtime.KeepAlive(kept)
	if !IsCollected(weak.Make(new([64]byte))) {
		t.Error("a value dropped stays reachable")
	}
}
