package api

import (
	"slices"
	"yt-music-player/internal/catalog"
)

// Ahead is a part of a reply too long for one frame, sent as a Reply of
// its own with the request's ID before the reply: gob keeps a connection's
// buffer as large as the largest frame it sent. Its lists go, in the order
// the parts came, before the reply's own (Reply.Join).
type Ahead struct {
	Songs     []catalog.Song
	Albums    []catalog.Album
	Artists   []catalog.Artist
	Playlists []catalog.Playlist
}

// lists points at the lists of r's result that may be long, nil for those
// it lacks; a Playlist's IDs stay, 5000 taking 60 KB.
type lists struct {
	songs     *[]catalog.Song
	albums    *[]catalog.Album
	artists   *[]catalog.Artist
	playlists *[]catalog.Playlist
}

func (r *Reply) lists() lists {
	switch {
	case r.Results != nil:
		return lists{songs: &r.Results.Songs, albums: &r.Results.Albums}
	case r.Album != nil:
		return lists{songs: &r.Album.Songs}
	case r.Playlist != nil:
		return lists{songs: &r.Playlist.Songs}
	case r.Library != nil:
		lib := r.Library
		return lists{songs: &lib.Songs, albums: &lib.Albums, artists: &lib.Artists, playlists: &lib.Playlists}
	}
	return lists{}
}

// Split moves r's lists ahead if they hold more than per items together:
// each whole, in parts of per items, or more so that all take about parts
// parts (one more per list but the first, at most). The parts are r's:
// they must not change until sent.
func (r *Reply) Split(per, parts int) []*Ahead {
	l := r.lists()
	n := size(l.songs) + size(l.albums) + size(l.artists) + size(l.playlists)
	if n <= per {
		return nil
	}
	per = max(per, (n+parts-1)/parts)
	count := chunks(l.songs, per) + chunks(l.albums, per) + chunks(l.artists, per) + chunks(l.playlists, per)
	// One slab for the parts, which go together.
	slab, ahead := make([]Ahead, count), make([]*Ahead, count)
	for i := range slab {
		ahead[i] = &slab[i]
	}
	next := split(ahead, l.songs, per, func(a *Ahead, s []catalog.Song) { a.Songs = s })
	next = split(next, l.albums, per, func(a *Ahead, s []catalog.Album) { a.Albums = s })
	next = split(next, l.artists, per, func(a *Ahead, s []catalog.Artist) { a.Artists = s })
	split(next, l.playlists, per, func(a *Ahead, s []catalog.Playlist) { a.Playlists = s })
	return ahead
}

func size[T any](list *[]T) int {
	if list == nil {
		return 0
	}
	return len(*list)
}

func chunks[T any](list *[]T, per int) int { return (size(list) + per - 1) / per }

// split puts list's chunks of per items into the first parts of ahead,
// and returns the parts left.
func split[T any](ahead []*Ahead, list *[]T, per int, put func(*Ahead, []T)) []*Ahead {
	if size(list) == 0 {
		return ahead
	}
	for part := range slices.Chunk(*list, per) {
		put(ahead[0], part)
		ahead = ahead[1:]
	}
	*list = nil
	return ahead
}

// Join puts back the lists that went ahead of r, in the order they came,
// before r's own. Those of a list r's result lacks are dropped.
func (r *Reply) Join(ahead []*Ahead) {
	if len(ahead) == 0 {
		return
	}
	l := r.lists()
	join(l.songs, ahead, func(a *Ahead) []catalog.Song { return a.Songs })
	join(l.albums, ahead, func(a *Ahead) []catalog.Album { return a.Albums })
	join(l.artists, ahead, func(a *Ahead) []catalog.Artist { return a.Artists })
	join(l.playlists, ahead, func(a *Ahead) []catalog.Playlist { return a.Playlists })
}

func join[T any](list *[]T, ahead []*Ahead, of func(*Ahead) []T) {
	if list == nil {
		return
	}
	n := len(*list)
	for _, a := range ahead {
		n += len(of(a))
	}
	if n == len(*list) {
		return
	}
	joined := make([]T, 0, n)
	for _, a := range ahead {
		joined = append(joined, of(a)...)
	}
	*list = append(joined, *list...)
}
