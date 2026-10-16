package remote

import (
	"testing"
	"yt-music-player/internal/budget"
)

// TestBudgetApplyEvents holds an event's way through the front-end's
// thread: queued by the reader, flushed into the Mirror. The reader's
// queue and the flushed batch swap, so neither grows again.
func TestBudgetApplyEvents(t *testing.T) {
	for _, tc := range applyCases {
		t.Run(tc.name, func(t *testing.T) {
			c, l := applying()
			ev := tc.event()
			budget.Allocs(t, tc.allocs, 100, func() {
				c.receive(l, ev)
				c.flush()
			})
		})
	}
}
