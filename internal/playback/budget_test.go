package playback

import (
	"testing"
	"yt-music-player/internal/budget"
	"yt-music-player/internal/player"
)

// TestBudgetFollow: Follow runs every batch of the daemon's.
func TestBudgetFollow(t *testing.T) {
	for _, tc := range followCases {
		t.Run(tc.name, func(t *testing.T) {
			x := playing(t)
			// 0 allocs today for each.
			budget.Allocs(t, 0, 100, func() {
				tc.ready(x)
				x.pb.Follow()
			})
		})
	}
}

// TestBudgetIdle: the daemon asks these every batch.
func TestBudgetIdle(t *testing.T) {
	for _, tc := range idleCases(playing(t)) {
		// 0 allocs today for each.
		t.Run(tc.name, func(t *testing.T) { budget.Allocs(t, 0, 100, tc.call) })
	}
}

func TestBudgetNext(t *testing.T) {
	for _, tc := range []struct {
		name       string
		prefetched bool
		limit      float64
	}{
		// 7 allocs today: the context, its cancel, the goroutine, the
		// result's way back, and the fake's note of the load.
		{"resolved", false, 9},
		// 1 alloc today: the fake's note of the load.
		{"prefetched", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := playing(t)
			setRepeat(x.player, player.RepeatAll)
			budget.Allocs(t, tc.limit, 100, func() { nextSong(x, tc.prefetched) })
		})
	}
}
