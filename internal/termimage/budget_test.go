package termimage

import (
	"testing"
	"yt-music-player/internal/budget"
)

// TestBudgetEncode holds encoding a cover, which the TUI does once per
// cover and size, off its UI thread.
func TestBudgetEncode(t *testing.T) {
	for _, tc := range encodeCases {
		t.Run(tc.name, func(t *testing.T) {
			m := cover(tc.px)
			budget.Allocs(t, tc.allocs, 20, func() { sink = tc.encode(m) })
		})
	}
}
