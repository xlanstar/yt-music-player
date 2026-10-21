package daemon

import (
	"testing"
	"yt-music-player/internal/account"
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
	for r := api.RepeatOff; r.Valid(); r++ {
		if !told[r] {
			t.Errorf("no player mode travels as %d", r)
		}
	}
	if player.Repeat(len(modes)).Valid() {
		t.Errorf("player mode %d has no row", len(modes))
	}
}

func TestWirePart(t *testing.T) {
	parts := map[account.Part]api.Part{
		account.Profile: api.PartProfile,
		account.Library: api.PartLibrary,
		account.Liked:   api.PartLiked,
	}
	for p, wire := range parts {
		if got := wirePart(p); got != wire {
			t.Errorf("%s travels as %d, want %d", p, got, wire)
		}
	}
	n := 0
	for wire := api.PartProfile; wire.Valid(); wire++ {
		if got := wirePart(accountPart(wire)); got != wire {
			t.Errorf("part %d comes back as %d", wire, got)
		}
		if _, ok := parts[accountPart(wire)]; !ok {
			t.Errorf("part %d is account's %d, which has no row", wire, accountPart(wire))
		}
		n++
	}
	if n != len(parts) {
		t.Errorf("%d parts on the wire, %d in account", n, len(parts))
	}
}
