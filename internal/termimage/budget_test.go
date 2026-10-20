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
			m := tc.source(t)
			budget.Allocs(t, tc.allocs, 20, func() { sink = tc.encode(m) })
		})
	}
}

// TestBudgetSplice holds splicing a frame, which ytm does for every frame
// the renderer writes, into the last frame's buffer.
func TestBudgetSplice(t *testing.T) {
	for _, tc := range spliceCases {
		t.Run(tc.name, func(t *testing.T) {
			frame, tab, w, h := tc.frame()
			dst := Splice(nil, frame, tab, w, h)
			budget.Allocs(t, tc.allocs, 100, func() { dst = Splice(dst[:0], frame, tab, w, h) })
		})
	}
}
