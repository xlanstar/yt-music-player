package main

import (
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/fetchstate"
	"yt-music-player/internal/settings"
)

// viewMemo keeps what builds derive from app state, remade only when its
// inputs change: a build runs every frame of a hover or scroll.
type viewMemo struct {
	// loc is the path and query q, by location: Router.Path and Query
	// parse on every call.
	loc        kept[string, [2]string]
	collection kept[collectionKey, collection]
	liked      kept[uint64, int]        // by the session's LikesGen
	likedLine  kept[uint64, string]     // by the session's LikesGen
	sidebar    kept[uint64, []sideItem] // by the catalog's Gen
	likedItem  sideItem
	home       kept[feedKey, [][]cardText]
	releases   kept[feedKey, []cardText]
	moods      kept[feedKey, []any] // the moods' keys, boxed
	// The library's cards: playlists by the catalog's Gen, as it keeps
	// them; albums and artists by the session's slices.
	libPlaylists kept[uint64, []cardText]
	libAlbums    kept[feedKey, []cardText]
	libArtists   kept[feedKey, []cardText]
	// typed is by the query typed, found by the query whose results show.
	typed      kept[string, typedText]
	found      kept[foundKey, foundText]
	holds      kept[holdsKey, bool]
	themesHint kept[settings.Dir, string]
	// noCredentials is whether the sign-in sheet's text cleans to nothing.
	noCredentials kept[string, bool]
	devtoolsStep  string
	rows          map[string]*rowText
}

// maxRowTexts bounds rows: a session sees a few thousand songs at most.
const maxRowTexts = 4096

type rowText struct {
	// from is the title, artist and album the strings were made from: a
	// song fetched anew may have changed.
	from [3]string
	// key is the ID boxed once: a string passed as an any key is copied
	// to the heap each time.
	key   any
	label string
	sub   string
}

func (a *app) rowText(s *catalog.Song) *rowText {
	from := [3]string{s.Title, s.Artist, s.Album}
	if t := a.memo.rows[s.ID]; t != nil && t.from == from {
		return t
	}
	if a.memo.rows == nil || len(a.memo.rows) >= maxRowTexts {
		a.memo.rows = make(map[string]*rowText)
	}
	t := &rowText{
		from:  from,
		key:   s.ID,
		label: songByArtist.say2(s.Title, s.Artist),
		sub:   s.Artist + " · " + s.Album,
	}
	a.memo.rows[s.ID] = t
	return t
}

// holdsKey identifies a collection's songs as feedKey does a feed, with
// the current song.
type holdsKey struct {
	first   *catalog.Song
	n       int
	current string
}

type typedText struct{ searching, none string }

type foundKey struct {
	query  string
	albums feedKey
}

type foundText struct {
	quoted string
	albums []cardText
}

type kept[K comparable, V any] struct {
	in    K
	v     V
	isSet bool
}

// stale reports whether v must be remade from in, and records in as the
// input it is made from.
func (k *kept[K, V]) stale(in K) bool {
	if k.isSet && k.in == in {
		return false
	}
	k.in, k.isSet = in, true
	return true
}

// collectionKey is what a collection page derives from; likes and owner only
// matter for Liked music.
type collectionKey struct {
	res          fetchstate.Resource
	music, likes uint64 // the catalog's Gen, the session's LikesGen
	owner        string
}

// feedKey identifies a feed's slice by its first element and length:
// fetching replaces a feed's slices, nothing changes them in place.
type feedKey struct {
	first any
	n     int
}

func keyOf[T any](s []T) feedKey {
	if len(s) == 0 {
		return feedKey{}
	}
	return feedKey{first: &s[0], n: len(s)}
}

type cardText struct {
	// key is catalog.Item.Key, boxed once like rowText.key.
	key any
	sub string // the line under the title
}

// path is a.router.Path(), parsed once per location.
func (a *app) path() string { return a.location()[0] }

// searchQuery is a.router.Query("q"), parsed once per location.
func (a *app) searchQuery() string { return a.location()[1] }

func (a *app) location() *[2]string {
	m := &a.memo.loc
	if m.stale(a.router.Location()) {
		m.v = [2]string{a.router.Path(), a.router.Query("q")} //nolint:forbidigo // The one place that parses, once a location.
	}
	return &m.v
}
