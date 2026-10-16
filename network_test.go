package main

import (
	"errors"
	"testing"
)

// Back online, a page that failed loads without a click on Retry.
func TestReconnectedRetries(t *testing.T) {
	src, a, q := scriptedApp()
	tt := newTester(a)
	a.router.Push(albumPath("remote"))
	answer(t, tt, q, src.Next(t, tt.Frame, "album", "remote"), reply{Err: errors.New("offline")})
	wantTexts(t, tt, "Couldn’t load this album")

	a.reconnected()
	answer(t, tt, q, src.Next(t, tt.Frame, "album", "remote"), reply{Album: remoteAlbum, Songs: remoteTracks})
	wantTexts(t, tt, "Far Signals", "Dead Air")
}
