package main

import (
	"errors"
	"testing"
	"yt-music-player/internal/remote"
)

// Back online, a page that failed loads without a click on Retry.
func TestReconnectedRetries(t *testing.T) {
	src, a, q := scriptedApp(t)
	tt := newTester(a)
	a.router.Push(albumPath("remote"))
	answer(t, tt, q, src.Next(t, tt.Frame, "album", "remote"), reply{Err: errors.New("offline")})
	wantTexts(t, tt, "Couldn’t load this album")

	a.reconnected()
	answer(t, tt, q, src.Next(t, tt.Frame, "album", "remote"), reply{Album: remoteAlbum, Songs: remoteTracks})
	wantTexts(t, tt, "Far Signals", "Dead Air")
}

// A page asked for before ytmd first answered loads once it does.
func TestFirstConnectionRetries(t *testing.T) {
	src, a, q := scriptedApp(t)
	tt := newTester(a)
	a.router.Push(albumPath("remote"))
	answer(t, tt, q, src.Next(t, tt.Frame, "album", "remote"), reply{Err: errors.New("disconnected")})
	wantTexts(t, tt, "Couldn’t load this album")

	a.daemon.link = remote.Link{}
	a.linked(remote.Link{Health: remote.Connected, Connection: 1})
	answer(t, tt, q, src.Next(t, tt.Frame, "album", "remote"), reply{Album: remoteAlbum, Songs: remoteTracks})
	wantTexts(t, tt, "Far Signals", "Dead Air")
}
