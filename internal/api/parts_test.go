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
// puts back in order, each part a frame of at most PartItems items, or
// PartSize's, the parts at most MaxParts plus one a list.
func TestSplitReply(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply func() *Reply
		// ahead is how many parts go ahead.
		ahead int
	}{
		{"short playlist", func() *Reply {
			return &Reply{Playlist: &PlaylistReply{Playlist: testPlaylist(), Songs: testSongs(PartItems)}}
		}, 0},
		{"long playlist", func() *Reply {
			return &Reply{Playlist: &PlaylistReply{Playlist: testPlaylist(), Songs: testSongs(PartItems + 1)}}
		}, 2},
		{"longest playlist", func() *Reply {
			return &Reply{Playlist: &PlaylistReply{Playlist: testPlaylist(), Songs: testSongs(2 * PartItems * MaxParts)}}
		}, MaxParts},
		{"long album", func() *Reply {
			return &Reply{Album: &AlbumReply{Album: testAlbum(), Songs: testSongs(2*PartItems + 1)}}
		}, 3},
		{"long results", func() *Reply {
			return &Reply{Results: &catalog.Results{Songs: testSongs(PartItems - 1), Albums: []catalog.Album{testAlbum(), testAlbum()}}}
		}, 2},
		// Each list goes ahead once the whole is too long.
		{"library", func() *Reply { return &Reply{Library: longLibrary(100, 90, 80, 70)} }, 4},
		{"long library", func() *Reply { return &Reply{Library: longLibrary(5000, 0, 300, 1)} }, MaxParts + 2},
		{"short library", func() *Reply { return &Reply{Library: longLibrary(4, 3, 2, 1)} }, 0},
		{"home", func() *Reply { return &Reply{Home: &catalog.Home{}} }, 0},
		{"error", func() *Reply { return &Reply{Err: &Error{Code: CodeNotFound}} }, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want, r := tc.reply(), tc.reply()
			ahead := r.Split()
			wl := want.lists()
			ahead = sendParts(t, ahead, size(wl.songs)+size(wl.albums)+size(wl.artists)+size(wl.playlists), tc.ahead)
			got := roundTrip(t, &Frame{ID: 3, Reply: r}).Reply
			got.Join(ahead)
			if !reflect.DeepEqual(got, want) {
				// Lengths: a reply of thousands of songs prints for long.
				gl := got.lists()
				t.Errorf("joined differs: %d %d %d %d items, want %d %d %d %d", size(gl.songs), size(gl.albums), size(gl.artists), size(gl.playlists),
					size(wl.songs), size(wl.albums), size(wl.artists), size(wl.playlists))
			}
		})
	}
}

// sendParts fails unless ahead are n parts of PartSize(items) items at
// most, and returns them as a receiver gets them, each in a frame of its
// own crossing a gob stream of its own.
func sendParts(t *testing.T, ahead []*Ahead, items, n int) []*Ahead {
	t.Helper()
	if len(ahead) != n {
		t.Errorf("%d parts ahead, want %d", len(ahead), n)
	}
	got := make([]*Ahead, len(ahead))
	for i, a := range ahead {
		if n := len(a.Songs) + len(a.Albums) + len(a.Artists) + len(a.Playlists); n == 0 || n > PartSize(items) {
			t.Errorf("part %d of %d items", i, n)
		}
		got[i] = roundTrip(t, &Frame{ID: 3, Ahead: a}).Ahead
	}
	return got
}

// A call's songs too many for one frame go in parts too; the caller's
// IDsArgs stay whole.
func TestSplitCall(t *testing.T) {
	for _, tc := range []struct {
		name  string
		songs int
		ahead int
	}{
		{"short", PartItems, 0},
		{"long", PartItems + 1, 2},
		{"longest", 2 * PartItems * MaxParts, MaxParts},
	} {
		t.Run(tc.name, func(t *testing.T) {
			songs := testSongs(tc.songs)
			ids := make([]string, len(songs))
			for i, s := range songs {
				ids[i] = s.ID
			}
			args := &IDsArgs{IDs: ids, Songs: songs, Start: 2, Next: true}
			whole := *args
			call := &Call{Method: MethodEnqueue, IDs: args}
			ahead := sendParts(t, call.Split(), tc.songs, tc.ahead)
			if !reflect.DeepEqual(*args, whole) {
				t.Error("the caller's IDsArgs changed")
			}
			got := roundTrip(t, &Frame{ID: 3, Call: call}).Call
			got.Join(ahead)
			if !reflect.DeepEqual(got, &Call{Method: MethodEnqueue, IDs: &whole}) {
				t.Errorf("joined %d songs of %d, %d IDs", len(got.IDs.Songs), tc.songs, len(got.IDs.IDs))
			}
		})
	}
	// Other calls carry no long list.
	call := &Call{Method: MethodSearch, Query: &QueryArgs{Query: "x"}}
	if ahead := call.Split(); ahead != nil {
		t.Errorf("a search in %d parts", len(ahead))
	}
}

// PartSize keeps parts of PartItems until a list would take more than
// MaxParts of them.
func TestPartSize(t *testing.T) {
	for _, tc := range []struct{ n, want int }{
		{0, PartItems},
		{PartItems * MaxParts, PartItems},
		{PartItems*MaxParts + 1, PartItems + 1},
		{5000, 313},
	} {
		if got := PartSize(tc.n); got != tc.want {
			t.Errorf("PartSize(%d) = %d, want %d", tc.n, got, tc.want)
		}
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
