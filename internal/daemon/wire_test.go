package daemon

import (
	"testing"
	"yt-music-player/internal/api"
	"yt-music-player/internal/player"
)

func TestWireRepeat(t *testing.T) {
	modes := []struct {
		mode player.Repeat
		wire api.Repeat
	}{
		{player.RepeatOff, api.RepeatOff},
		{player.RepeatAll, api.RepeatAll},
		{player.RepeatOne, api.RepeatOne},
	}
	told := map[api.Repeat]bool{}
	for _, tc := range modes {
		if got := wireRepeat(tc.mode); got != tc.wire {
			t.Errorf("mode %d travels as %d, want %d", tc.mode, got, tc.wire)
		}
		told[tc.wire] = true
	}
	for r := api.RepeatOff; r.IsValid(); r++ {
		if !told[r] {
			t.Errorf("no player mode travels as %d", r)
		}
	}
	if player.Repeat(len(modes)).IsValid() {
		t.Errorf("player mode %d has no row", len(modes))
	}
}
