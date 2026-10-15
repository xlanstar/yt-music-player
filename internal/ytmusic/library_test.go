package ytmusic

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"yt-music-player/internal/catalog"
)

const (
	playlistsKey = `browse {"browseId":"FEmusic_liked_playlists"}`
	albumsKey    = `browse {"browseId":"FEmusic_liked_albums"}`
	artistsKey   = `browse {"browseId":"FEmusic_library_corpus_track_artists"}`
	songsKey     = `browse {"browseId":"FEmusic_liked_videos"}`
)

func TestLibrary(t *testing.T) {
	c, s := signedIn(t, nil)
	lib, err := c.Library(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	// Liked music and Episodes for later are left to the UI; the second
	// page follows the first.
	wantPlaylists := []catalog.Playlist{
		{ID: "PLfakeplaylist0001", Name: "Road Trip (Fake)", Owner: "Jane Placeholder"},
		{ID: "PLfakeplaylist0002", Name: "Rainy Day Placeholder", Owner: "Fictional Curator"},
		{ID: "PLfakeplaylist0003", Name: "Late Night Pretend Mix", Owner: "Jane Placeholder"},
	}
	if len(lib.Playlists) != len(wantPlaylists) {
		t.Fatalf("playlists = %+v", lib.Playlists)
	}
	for i, pl := range lib.Playlists {
		if pl.Art != catalog.Art("https://lh3.googleusercontent.com/fake-"+pl.ID+"=w544-h544-l90-rj") {
			t.Errorf("playlist %d art = %q", i, pl.Art)
		}
		pl.Art = ""
		if !equalPlaylist(pl, wantPlaylists[i]) {
			t.Errorf("playlist %d = %+v, want %+v", i, pl, wantPlaylists[i])
		}
	}

	wantAlbums := []catalog.Album{
		{
			ID: "MPREb_fakealbum0001", Title: "Pretend Album", Artist: "The Fictional Band",
			Year: 2019, Kind: catalog.KindAlbum,
		},
		{
			ID: "MPREb_fakealbum0002", Title: "Imaginary EP", Artist: "Example Singer",
			Year: 2023, Kind: catalog.KindEP,
		},
	}
	if len(lib.Albums) != 2 || withoutAlbumArt(lib.Albums[0]) != wantAlbums[0] ||
		withoutAlbumArt(lib.Albums[1]) != wantAlbums[1] {
		t.Errorf("albums = %+v", lib.Albums)
	}

	// The uploader's channel is no artist; the continuation brings the
	// second.
	wantArtists := []catalog.Artist{
		{
			ID:   "UCfakeartist0001",
			Name: "The Fictional Band",
			Art:  "https://lh3.googleusercontent.com/fake-artist-1=w120-h120-l90-rj",
		},
		{
			ID:   "UCfakeartist0002",
			Name: "Example Singer",
			Art:  "https://lh3.googleusercontent.com/fake-artist-2=w120-h120-l90-rj",
		},
	}
	if !slices.Equal(lib.Artists, wantArtists) {
		t.Errorf("artists = %+v", lib.Artists)
	}

	// The shuffle row is skipped.
	var ids []string
	for _, s := range lib.Songs {
		ids = append(ids, s.ID)
	}
	if !slices.Equal(ids, []string{"fakevideo01", "fakevideo02", "fakevideo03"}) {
		t.Errorf("songs = %v", ids)
	}
	wantSong := catalog.Song{
		ID: "fakevideo01", Title: "Imaginary Song", Artist: "The Fictional Band",
		Album: "Pretend Album", AlbumID: "MPREb_fakealbum0001", Seconds: 205,
	}
	if len(lib.Songs) > 0 && withoutArt(lib.Songs[0]) != wantSong {
		t.Errorf("song 0 = %+v, want %+v", lib.Songs[0], wantSong)
	}

	served := s.served()
	var keys []string
	for _, r := range served {
		keys = append(keys, r.key)
		if r.session != live {
			t.Errorf("library request not signed: %v", r)
		}
	}
	slices.Sort(keys)
	want := []string{
		`browse {"browseId":"FEmusic_library_corpus_track_artists"}`,
		`browse {"browseId":"FEmusic_liked_albums"}`,
		`browse {"browseId":"FEmusic_liked_playlists"}`,
		`browse {"browseId":"FEmusic_liked_videos"}`,
		`browse {"continuation":"FAKE_ARTISTS_CONTINUATION"}`,
		`browse {"continuation":"FAKE_PLAYLISTS_CONTINUATION"}`,
		`browse {"continuation":"FAKE_SONGS_CONTINUATION"}`,
	}
	if !slices.Equal(keys, want) {
		t.Errorf("requests = %v, want %v", keys, want)
	}
}

func equalPlaylist(a, b catalog.Playlist) bool {
	return a.ID == b.ID && a.Name == b.Name && a.Owner == b.Owner && a.Art == b.Art && slices.Equal(a.Songs, b.Songs)
}

func TestLibraryPartialFailure(t *testing.T) {
	c, s := signedIn(t, nil)
	s.failWith(albumsKey, http.StatusInternalServerError)
	s.failWith(`browse {"continuation":"FAKE_SONGS_CONTINUATION"}`, http.StatusServiceUnavailable)
	lib, err := c.Library(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(lib.Albums) != 0 || len(lib.Playlists) != 3 || len(lib.Artists) != 2 {
		t.Errorf("got %d playlists, %d albums, %d artists; want 3, 0, 2",
			len(lib.Playlists), len(lib.Albums), len(lib.Artists))
	}
	// A failed continuation keeps the pages before it.
	if len(lib.Songs) != 2 {
		t.Errorf("got %d songs, want the first page's 2", len(lib.Songs))
	}
}

func TestLibraryFailsWhenEveryPartFails(t *testing.T) {
	c, s := signedIn(t, nil)
	for _, key := range []string{playlistsKey, albumsKey, artistsKey, songsKey} {
		s.failWith(key, http.StatusInternalServerError)
	}
	if _, err := c.Library(t.Context()); err == nil || errors.Is(err, catalog.ErrSignedOut) {
		t.Errorf("err = %v, want a failure other than ErrSignedOut", err)
	}
}

func TestLibrarySignedOutPart(t *testing.T) {
	c, s := signedIn(t, nil)
	s.failWith(artistsKey, http.StatusUnauthorized)
	if _, err := c.Library(t.Context()); !errors.Is(err, catalog.ErrSignedOut) {
		t.Errorf("err = %v, want ErrSignedOut", err)
	}
}

func TestEmptyLibrary(t *testing.T) {
	c := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "testdata/signed_in/empty_library.json")
	})
	c.api.Auth.Set(mustParse(t, testCookie).Decorator(nil))
	lib, err := c.Library(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(lib.Playlists)+len(lib.Albums)+len(lib.Artists)+len(lib.Songs) != 0 {
		t.Errorf("library = %+v, want empty", lib)
	}
}

func TestLibraryCaps(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		rowsPerPage          int
		wantPages, wantSongs int
	}{
		{"items", 50, maxLibraryItems / 50, maxLibraryItems},
		// Pages without rows that keep giving tokens.
		{"pages", 0, maxLibraryPages, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var pages atomic.Int32
			rows := strings.TrimSuffix(strings.Repeat(songRowJSON+",", tc.rowsPerPage), ",")
			c := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
				_, body := readRequest(t, r)
				if id := body["browseId"]; id != nil && id != librarySongs {
					http.ServeFile(w, r, "testdata/signed_in/empty_library.json")
					return
				}
				// Every page brings a new token: the feed never ends.
				shelf := fmt.Sprintf(`{"contents":[%s],`+
					`"continuations":[{"nextContinuationData":{"continuation":"t%d"}}]}`, rows, pages.Add(1))
				if body["browseId"] != nil {
					_, _ = fmt.Fprintf(w, `{"contents":{"singleColumnBrowseResultsRenderer":{"tabs":[{"tabRenderer":`+
						`{"content":{"sectionListRenderer":{"contents":[{"musicShelfRenderer":%s}]}}}}]}}}`, shelf)
					return
				}
				_, _ = fmt.Fprintf(w, `{"continuationContents":{"musicShelfContinuation":%s}}`, shelf)
			})
			c.api.Auth.Set(mustParse(t, testCookie).Decorator(nil))
			lib, err := c.Library(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if int(pages.Load()) != tc.wantPages || len(lib.Songs) != tc.wantSongs {
				t.Errorf("%d pages, %d songs; want %d, %d", pages.Load(), len(lib.Songs), tc.wantPages, tc.wantSongs)
			}
		})
	}
}

const songRowJSON = `{"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"fakevideo01"},` +
	`"flexColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Imaginary Song"}]}}}]}}`
