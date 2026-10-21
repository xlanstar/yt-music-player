package api

import (
	"reflect"
	"strconv"
	"testing"
	"yt-music-player/internal/catalog"
)

func testSongs(n int) []catalog.Song {
	songs := make([]catalog.Song, n)
	for i := range songs {
		songs[i] = testSong(i)
	}
	return songs
}

// repeated returns n items f makes, nil for none, as gob decodes them.
func repeated[T any](n int, f func(i int) T) []T {
	if n == 0 {
		return nil
	}
	out := make([]T, n)
	for i := range out {
		out[i] = f(i)
	}
	return out
}

func longLibrary(songs, albums, artists, playlists int) *catalog.Library {
	return &catalog.Library{
		Songs: testSongs(songs),
		Albums: repeated(albums, func(i int) catalog.Album {
			a := testAlbum()
			a.ID += strconv.Itoa(i)
			return a
		}),
		Artists: repeated(artists, func(i int) catalog.Artist {
			a := testArtist()
			a.ID += strconv.Itoa(i)
			return a
		}),
		Playlists: repeated(playlists, func(i int) catalog.Playlist {
			p := testPlaylist()
			p.ID += strconv.Itoa(i)
			return p
		}),
	}
}

// A reply too long for one frame goes in parts, whose lists the receiver
// puts back in order, each part a frame of at most per items, the parts
// at most parts plus one a list.
func TestSplitReply(t *testing.T) {
	const per, parts = 10, 4
	for _, tc := range []struct {
		name  string
		reply func() *Reply
		// ahead is how many parts go ahead.
		ahead int
	}{
		{"short playlist", func() *Reply {
			return &Reply{Playlist: &PlaylistReply{Playlist: testPlaylist(), Songs: testSongs(per)}}
		}, 0},
		{"long playlist", func() *Reply {
			return &Reply{Playlist: &PlaylistReply{Playlist: testPlaylist(), Songs: testSongs(per + 1)}}
		}, 2},
		{"longest playlist", func() *Reply {
			return &Reply{Playlist: &PlaylistReply{Playlist: testPlaylist(), Songs: testSongs(1000)}}
		}, parts},
		{"long album", func() *Reply {
			return &Reply{Album: &AlbumReply{Album: testAlbum(), Songs: testSongs(25)}}
		}, 3},
		{"long results", func() *Reply {
			return &Reply{Results: &catalog.Results{Songs: testSongs(9), Albums: []catalog.Album{testAlbum(), testAlbum()}}}
		}, 2},
		// Each list goes ahead once the whole is too long.
		{"library", func() *Reply { return &Reply{Library: longLibrary(5, 4, 3, 2)} }, 4},
		{"long library", func() *Reply { return &Reply{Library: longLibrary(100, 0, 31, 1)} }, parts + 2},
		{"short library", func() *Reply { return &Reply{Library: longLibrary(4, 3, 2, 1)} }, 0},
		{"home", func() *Reply { return &Reply{Home: &catalog.Home{}} }, 0},
		{"error", func() *Reply { return &Reply{Err: &Error{Code: CodeNotFound}} }, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want, r := tc.reply(), tc.reply()
			ahead := r.Split(per, parts)
			if len(ahead) != tc.ahead {
				t.Errorf("%d parts ahead, want %d", len(ahead), tc.ahead)
			}
			for i, a := range ahead {
				n := len(a.Songs) + len(a.Albums) + len(a.Artists) + len(a.Playlists)
				if n == 0 || n > max(per, 1000/parts) {
					t.Errorf("part %d of %d items", i, n)
				}
				// Each part crosses a gob stream of its own.
				ahead[i] = roundTrip(t, &Frame{ID: 3, Reply: &Reply{Ahead: a}}).Reply.Ahead
			}
			got := roundTrip(t, &Frame{ID: 3, Reply: r}).Reply
			got.Join(ahead)
			if !reflect.DeepEqual(got, want) {
				// Lengths: a reply of a thousand songs prints for long.
				gl, wl := got.lists(), want.lists()
				t.Errorf("joined differs: %d %d %d %d items, want %d %d %d %d", size(gl.songs), size(gl.albums), size(gl.artists), size(gl.playlists),
					size(wl.songs), size(wl.albums), size(wl.artists), size(wl.playlists))
			}
		})
	}
}

// Join leaves alone a reply that came whole, and a list its result lacks.
func TestJoinReply(t *testing.T) {
	r := &Reply{Home: &catalog.Home{}}
	r.Join([]*Ahead{{Songs: testSongs(2)}})
	if !reflect.DeepEqual(r, &Reply{Home: &catalog.Home{}}) {
		t.Errorf("home joined to songs: %+v", r)
	}
	songs := testSongs(3)
	r = &Reply{Album: &AlbumReply{Songs: songs[2:]}}
	r.Join([]*Ahead{{Songs: songs[:1]}, {Songs: songs[1:2]}})
	if !reflect.DeepEqual(r.Album.Songs, songs) {
		t.Errorf("album songs %+v", r.Album.Songs)
	}
	r.Join(nil)
	if !reflect.DeepEqual(r.Album.Songs, songs) {
		t.Errorf("album songs %+v after joining nothing", r.Album.Songs)
	}
}
