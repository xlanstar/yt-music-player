package catalog

import (
	"slices"
	"strconv"
	"testing"
	"yt-music-player/internal/budget"
)

func TestBudgetNew(t *testing.T) {
	for _, tc := range []struct {
		songs int
		limit float64
	}{
		{18, 13},       // 11 allocs today.
		{bigSongs, 29}, // 25 allocs today: the indexes grow with the songs.
	} {
		t.Run(strconv.Itoa(tc.songs), func(t *testing.T) {
			songs, albums, playlists := scaledSongs(tc.songs), placeholderAlbums(), placeholderPlaylists()
			budget.Allocs(t, tc.limit, 10, func() { sinkCatalog = New(songs, albums, playlists) })
		})
	}
}

func TestBudgetPlaceholder(t *testing.T) {
	// 34 allocs today.
	budget.Allocs(t, 40, 20, func() { sinkCatalog = Placeholder() })
}

func TestBudgetAdd(t *testing.T) {
	t.Run("fresh", func(t *testing.T) {
		c, pages := bigCatalog(), freshPages(101)
		page := 0
		// 0.10 allocs today, 0.01 to 0.22 over 200 runs: the songs grow once
		// in these 100 pages, their index's tables as often as its hash
		// seed, random per map, fills them.
		budget.Allocs(t, 0.25, 100, func() {
			c.Add(pages[page], nil, nil)
			page++
		})
	})
	t.Run("known", func(t *testing.T) {
		c := bigCatalog()
		page := slices.Clone(c.Songs[100:120])
		// 0 allocs today: replaced in place.
		budget.Allocs(t, 0, 100, func() { c.Add(page, nil, nil) })
	})
	t.Run("feed_playlists", func(t *testing.T) {
		c := bigCatalog()
		mixes := placeholderMixes()
		for i := range mixes {
			mixes[i].Songs = nil
		}
		// 1 alloc today: the library's playlists, reindexed at their size.
		budget.Allocs(t, 1, 100, func() { c.Add(nil, nil, mixes) })
	})
}

func TestBudgetAddAlbum(t *testing.T) {
	c := bigCatalog()
	album, tracks := benchAlbum()
	// 1 alloc today: the track order.
	budget.Allocs(t, 1, 100, func() { c.AddAlbum(album, tracks) })
}

func TestBudgetAlbumArtists(t *testing.T) {
	albums := Placeholder().Albums
	// 8 allocs today: the seen set, and the artists growing.
	budget.Allocs(t, 10, 100, func() { sinkArtists = AlbumArtists(albums) })
}

func TestBudgetSearch(t *testing.T) {
	for _, tc := range searchCases {
		t.Run(tc.name, func(t *testing.T) {
			src := newPlaceholderSource(tc.cat(), 0)
			budget.Allocs(t, tc.limit, 20, func() { sinkResults = src.search(tc.query) })
		})
	}
}

func TestBudgetLookups(t *testing.T) {
	c := bigCatalog()
	ids := c.SongIDs()
	for _, tc := range []struct {
		name  string
		limit float64
		call  func()
	}{
		// None allocates except for the slice it returns.
		{"Song", 0, func() { sinkSong, sinkBool = c.Song("ember-4000") }},
		{"SongsByID/10", 1, func() { sinkSongs = c.SongsByID(ids[:10]) }},
		{"SongsByID/2000", 1, func() { sinkSongs = c.SongsByID(ids[:2000]) }},
		{"SongIDs", 1, func() { sinkIDs = c.SongIDs() }},
		{"AlbumTracks/track_order", 1, func() { sinkSongs = c.AlbumTracks("MPREb_bench") }},
		{"AlbumTracks/catalog_order", 2, func() { sinkSongs = c.AlbumTracks("neon-tide") }},
		{"Album", 0, func() { sinkAlbum, sinkBool = c.Album("northbound") }},
		{"Playlist", 0, func() { sinkPlaylist, sinkBool = c.Playlist("deep-focus") }},
		{"LibraryPlaylists", 0, func() { sinkPlaylists = c.LibraryPlaylists() }},
		{"TotalSeconds", 0, func() { sinkInt = TotalSeconds(c.Songs[:2000]) }},
	} {
		t.Run(tc.name, func(t *testing.T) { budget.Allocs(t, tc.limit, 20, tc.call) })
	}
}

func TestBudgetSetLibrary(t *testing.T) {
	c := bigCatalog()
	ids := slices.Clone(c.libraryIDs)
	// 1 alloc today: the playlists the IDs index; the IDs keep their array.
	budget.Allocs(t, 1, 100, func() { c.SetLibrary(ids) })
}

func TestBudgetPlaceholderSource(t *testing.T) {
	for _, tc := range placeholderCalls(t.Context(), NewPlaceholderSource(0)) {
		t.Run(tc.name, func(t *testing.T) { budget.Allocs(t, tc.limit, 50, tc.call) })
	}
}

func TestBudgetKeepSongs(t *testing.T) {
	songs := scaledSongs(bigSongs)
	keep := func(i int) bool { return i != bigSongs/2 }
	c := New(slices.Clone(songs), nil, nil)
	// 1 alloc today: the songs kept; the index is cleared in place.
	budget.Allocs(t, 1, 20, func() {
		c.KeepSongs(keep)
		c.Add([]Song{songs[bigSongs/2]}, nil, nil)
	})
}

func TestBudgetShareIDs(t *testing.T) {
	songs, ids := decodedIDs(bigSongs)
	// 0 allocs today.
	budget.Allocs(t, 0, 10, func() { ShareIDs(ids, songs) })
}

func TestBudgetShareStrings(t *testing.T) {
	album := decodedTracks()
	// 4 allocs today: the map of its few strings.
	budget.Allocs(t, 5, 100, func() { ShareStrings(album) })
	songs := scaledSongs(bigSongs)
	// 18 allocs today, 328 KB: the map, sized for as many strings as
	// songs.
	budget.Within(t, budget.Cost{Allocs: 21, Bytes: 378e3}, 10, func() { ShareStrings(songs) })
}
