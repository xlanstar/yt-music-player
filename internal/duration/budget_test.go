package duration

import (
	"testing"
	"yt-music-player/internal/budget"
)

func TestBudgetClock(t *testing.T) {
	for _, tc := range clockCases {
		t.Run(tc.name, func(t *testing.T) {
			budget.Allocs(t, tc.limit, 100, func() { sinkString = Clock(tc.seconds) })
		})
	}
}
