package main

import (
	"testing"
	"yt-music-player/internal/catalog"
)

// TestClock: the app says lengths of time as the TUI and ytm do, an hour
// and more as h:mm:ss.
func TestClock(t *testing.T) {
	for in, want := range map[int]string{0: "0:00", 227: "3:47", 3599: "59:59", 3600: "1:00:00", 4512: "1:15:12"} {
		if got := clock(in); got != want {
			t.Errorf("clock(%d) = %q, want %q", in, got, want)
		}
	}
}

// TestTotalLength: a collection's minutes round to the nearest, as in the
// TUI.
func TestTotalLength(t *testing.T) {
	songs := []catalog.Song{{ID: "a", Seconds: 50}, {ID: "b", Seconds: 40}}
	if got, want := totalLength(songs), "· 2 songs · 2 min"; got != want {
		t.Errorf("totalLength = %q, want %q", got, want)
	}
}
