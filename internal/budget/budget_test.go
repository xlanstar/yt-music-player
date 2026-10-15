package budget

import "testing"

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
