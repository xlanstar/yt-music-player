package main

import (
	"testing"
	"yt-music-player/internal/catalog"
)

const likedEmpty = "Songs you like will show up here."

// Only Liked music says what fills it: likes fill no other playlist or
// album.
func TestEmptyPlaylist(t *testing.T) {
	src, a, q := scriptedApp(t)
	tt := newTester(a)
	a.router.Push(playlistPath("blank"))
	pl := catalog.Playlist{ID: "blank", Name: "Blank Tape", Owner: "Lan"}
	answer(t, tt, q, src.Next(t, tt.Frame, "playlist", "blank"), reply{Playlist: pl})
	if !tt.HasText("Blank Tape") || !tt.HasText("Nothing here yet") || tt.HasText(likedEmpty) {
		t.Errorf("empty playlist wrong, texts %q", tt.Texts())
	}
}
