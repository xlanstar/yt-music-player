package budget

import (
	"runtime/metrics"
	"testing"
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
