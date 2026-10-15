package audio

import (
	"testing"
	"yt-music-player/internal/budget"
)

func TestBudgetFake(t *testing.T) {
	f := NewFake(func(e Event) { sinkEvent = e })
	for _, tc := range fakeCalls(f) {
		t.Run(tc.name, func(t *testing.T) {
			budget.Allocs(t, tc.limit, 100, func() {
				tc.call()
				f.Calls = f.Calls[:0]
			})
		})
	}
}

func TestBudgetHalvedEnd(t *testing.T) {
	// 0 allocs today.
	budget.Allocs(t, 0, 100, func() { sinkFloat = halvedEnd(426.2, 213.1) })
}
