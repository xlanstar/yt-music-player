package ytmusic

import (
	"bytes"
	"cmp"
	"testing"
	"yt-music-player/internal/budget"
)

func TestBudgetDecode(t *testing.T) {
	data := readBytes(t, "album.json")
	var r bytes.Reader
	// 372 allocs, 28.3 KB today (read whole first: 389, 117 KB): every
	// response is decoded, its bytes held until parsed.
	budget.Within(t, budget.Cost{Allocs: 429, Bytes: 32.6e3}, 20, func() {
		r.Reset(data)
		sinkResponse, sinkErr = decode(&r)
	})
}

func TestBudgetSearchSongs(t *testing.T) {
	resp := decoded(t, "search_songs.json")
	// 5 allocs today, for 20 songs: the slice, and the songs of several
	// artists, whose names are joined.
	budget.Allocs(t, 6, 100, func() { sinkSongs = parseRows(searchRows(resp), parseSong) })
}

func TestBudgetSearchAlbums(t *testing.T) {
	resp := decoded(t, "search_albums.json")
	// 2 allocs today, for 20 albums.
	budget.Allocs(t, 2, 100, func() { sinkAlbums = parseRows(searchRows(resp), parseAlbumRow) })
}

func TestBudgetAlbumPage(t *testing.T) {
	resp := decoded(t, "album.json")
	// 5 allocs today.
	budget.Allocs(t, 6, 100, func() { sinkAlbum, sinkSongs, sinkBool = albumPage(albumID, resp) })
}

func TestBudgetPlaylistPage(t *testing.T) {
	resp := decoded(t, "playlist.json")
	// 0 allocs today.
	budget.Allocs(t, 0, 100, func() { sinkPlaylist, sinkRows, sinkBool = playlistPage(playlistID, resp) })
}

func TestBudgetPlaylistSongs(t *testing.T) {
	first := playlistRows(t)
	for _, tc := range []struct {
		name  string
		rows  []item
		limit float64
	}{
		// 1 alloc today: the slice; a song of one artist takes none.
		{"first_page", first, 1},
		// 1 today.
		{"continuation", continuationItems(decoded(t, "playlist_continuation.json")), 1},
		// 1 today.
		{"2000_songs", scaledRows(songRows(first), 2000), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			budget.Allocs(t, tc.limit, 20, func() { sinkSongs = playlistSongs(nil, tc.rows) })
		})
	}
}

func TestBudgetContinuation(t *testing.T) {
	rows := playlistRows(t)
	// 0 allocs today.
	budget.Allocs(t, 0, 100, func() { sinkString = continuation(rows) })
}

func TestBudgetParseShelves(t *testing.T) {
	for _, tc := range []struct {
		name, file string
		limit      float64
	}{
		{"home", "home.json", 14},                          // 12 allocs today.
		{"continuation_1", "home_continuation_1.json", 97}, // 84 today.
		{"continuation_2", "home_continuation_2.json", 9},  // 7 today.
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := decoded(t, tc.file)
			sections := cmp.Or(sectionList(resp), resp.ContinuationContents.SectionList).sections()
			budget.Allocs(t, tc.limit, 100, func() { sinkShelves = parseShelves(sections) })
		})
	}
}

// TestBudgetLibraryRows parses every row of the library's first pages,
// as Library does.
func TestBudgetLibraryRows(t *testing.T) {
	var items, songs []item
	for _, part := range []string{"liked_playlists", "liked_albums", "library_artists"} {
		items = append(items, libraryContents(decoded(t, "signed_in/"+part+".json")).rows()...)
	}
	songs = libraryContents(decoded(t, "signed_in/library_songs.json")).rows()
	// 0 allocs today, for 13 rows.
	budget.Allocs(t, 0, 100, func() {
		for _, r := range items {
			sinkItem, sinkBool = parseItem(r)
		}
		for _, r := range songs {
			sinkSong, sinkBool = parseSong(r)
		}
	})
}

func TestBudgetExplorePage(t *testing.T) {
	resp := decoded(t, "explore.json")
	// 17 allocs today.
	budget.Allocs(t, 20, 100, func() { sinkAlbums, sinkSongs = explorePage(resp) })
}

func TestBudgetMoodsPage(t *testing.T) {
	resp := decoded(t, "moods_and_genres.json")
	// 11 allocs today.
	budget.Allocs(t, 13, 100, func() { sinkMoods = moodsPage(resp) })
}

func TestBudgetChartPage(t *testing.T) {
	resp := decoded(t, "charts.json")
	// 6 allocs today.
	budget.Allocs(t, 7, 100, func() { sinkSongs, sinkString = chartPage(resp) })
}

func TestBudgetAccountMenu(t *testing.T) {
	resp := decoded(t, "signed_in/account_menu.json")
	// 0 allocs today.
	budget.Allocs(t, 0, 100, func() { sinkAccount, sinkBool = accountMenu(resp) })
}

func TestBudgetSignedOut(t *testing.T) {
	for _, file := range []string{"signed_in/liked_music.json", "signed_in/signed_out_page.json"} {
		resp := decoded(t, file)
		// 0 allocs today.
		budget.Allocs(t, 0, 100, func() { sinkBool = signedOut(resp) })
	}
}

// TestBudgetHelpers holds the helpers every parser calls per row to no
// allocation, or one where a result needs it.
func TestBudgetHelpers(t *testing.T) {
	row := songRows(playlistRows(t))[0].ListItem
	search := songRows(searchRows(decoded(t, "search_songs.json")))
	subtitleRuns := skipLabel(search[0].ListItem.column(1), typeLabels)
	albumRuns := searchRows(decoded(t, "search_albums.json"))[0].ListItem.column(1)
	for _, tc := range []struct {
		name  string
		limit float64
		f     func()
	}{
		{"videoID", 0, func() { sinkString = row.videoID() }},
		{"art", 0, func() { sinkArt = row.art() }},
		{"seconds", 0, func() { sinkInt = seconds("1:02:03") + seconds("Daft Punk") }},
		{"runs_one", 0, func() { sinkString = row.column(0).String() }},
		// 1 alloc today: the artists.
		{"meta_read", 1, func() {
			var m meta
			m.read(subtitleRuns, true)
			sinkString = m.artist()
		}},
		// 1 alloc today: the artists.
		{"subtitle", 1, func() { sinkKind, sinkString, sinkInt = subtitle(albumRuns) }},
		{"tint", 0, func() { sinkTint = tint(0xff1db954) }},
	} {
		t.Run(tc.name, func(t *testing.T) { budget.Allocs(t, tc.limit, 100, tc.f) })
	}
}
