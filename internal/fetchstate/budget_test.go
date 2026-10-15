package fetchstate

import (
	"testing"
	"yt-music-player/internal/budget"
)

func TestBudgetState(t *testing.T) {
	r := benchRegistry(20)
	for _, tc := range []struct {
		name string
		call func()
	}{
		// 0 allocs today for each.
		{"State/loaded", func() { sinkFetch = r.State(benchLoaded) }},
		{"State/idle", func() { sinkFetch = r.State(benchIdle) }},
		{"Set", func() {
			r.Set(benchLoaded, Fetch{State: Loading})
			r.Set(benchLoaded, Fetch{State: Loaded})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) { budget.Allocs(t, 0, 100, tc.call) })
	}
}
