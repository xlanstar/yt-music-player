package tui

import "yt-music-player/internal/catalog"

// memo holds what renders derive, made again only when its inputs change.
type memo struct {
	songs  songsMemo
	liked  likedMemo
	volume volumeMemo
}

type songsKey struct {
	o                opened
	catGen, likesGen uint64
}

type songsMemo struct {
	key   songsKey
	songs []catalog.Song
	ok    bool
}

type likedMemo struct {
	gen  uint64
	text string
	ok   bool
}

type volumeMemo struct {
	percent int
	text    string
	muted   bool
	ok      bool
}
