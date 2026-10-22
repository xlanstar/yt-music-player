package catalog

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Sinks keep the compiler from optimizing measured calls away.
var (
	sinkCatalog   *Catalog
	sinkSong      Song
	sinkSongs     []Song
	sinkAlbum     Album
	sinkPlaylist  Playlist
	sinkPlaylists []Playlist
	sinkArtists   []Artist
	sinkIDs       []string
	sinkResults   Results
	sinkHome      Home
	sinkExplore   Explore
	sinkAccount   Account
	sinkLibrary   Library
	sinkInt       int
	sinkBool      bool
	sinkErr       error
)

// bigSongs is a large library's song count; search and scanning lookups
// scale with it.
const bigSongs = 5000

// scaledSongs repeats the placeholder songs up to count; copies get their
// own ID and album, as in a large library.
func scaledSongs(count int) []Song {
	small := placeholderSongs()
	songs := make([]Song, count)
	for i := range songs {
		song := small[i%len(small)]
		if i >= len(small) {
			song.ID += "-" + strconv.Itoa(i)
			if song.AlbumID != "" {
				song.AlbumID += "-" + strconv.Itoa(i)
			}
		}
		songs[i] = song
	}
	return songs
}

// bigCatalog is the placeholder grown to bigSongs songs, with the
// placeholder's library and one fetched album.
func bigCatalog() *Catalog {
	small := Placeholder()
	c := New(scaledSongs(bigSongs), slices.Clone(small.Albums), slices.Clone(small.Playlists))
	c.SetLibrary(small.libraryIDs)
	c.AddAlbum(benchAlbum())
	return c
}

func benchAlbum() (Album, []Song) {
	album := Album{
		ID:     "MPREb_bench",
		Title:  "Bench Album",
		Artist: "Bench Artist",
		Year:   2026,
		Kind:   ReleaseAlbum,
		Art:    "neon-tide",
	}
	tracks := make([]Song, 12)
	for i := range tracks {
		num := strconv.Itoa(i)
		tracks[i] = Song{
			ID:      "bench-track-" + num,
			Title:   "Track " + num,
			Artist:  album.Artist,
			Album:   album.Title,
			AlbumID: album.ID,
			Seconds: 200 + i,
			Art:     album.Art,
		}
	}
	return album, tracks
}

// freshPages returns pages of 20 unseen songs, as a search or playlist
// fetch brings.
func freshPages(count int) [][]Song {
	pages := make([][]Song, count)
	for p := range pages {
		pages[p] = make([]Song, 20)
		for i := range pages[p] {
			id := "fresh-" + strconv.Itoa(p) + "-" + strconv.Itoa(i)
			pages[p][i] = Song{
				ID: id, Title: "Fresh " + id, Artist: "Fresh Artist", Album: "Fresh Album", Seconds: 180,
			}
		}
	}
	return pages
}

func BenchmarkNew(b *testing.B) {
	for _, count := range []int{18, bigSongs} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			songs, albums, playlists := scaledSongs(count), placeholderAlbums(), placeholderPlaylists()
			b.ReportAllocs()
			for b.Loop() {
				sinkCatalog = New(songs, albums, playlists)
			}
		})
	}
}

func BenchmarkPlaceholder(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		sinkCatalog = Placeholder()
	}
}

// BenchmarkAdd adds a page of fetched songs to a large library, as each
// search or page fetch does on the UI thread: unseen songs are appended,
// known ones replaced in place.
func BenchmarkAdd(b *testing.B) {
	b.Run("fresh", func(b *testing.B) {
		// The catalog grows a page per call, so it is rebuilt now and then,
		// outside the timer, to stay library-sized.
		pages := freshPages(250)
		base := bigCatalog()
		var c *Catalog
		b.ReportAllocs()
		call := 0
		for b.Loop() {
			if call%len(pages) == 0 {
				b.StopTimer()
				c = New(slices.Clone(base.Songs), slices.Clone(base.Albums), slices.Clone(base.Playlists))
				b.StartTimer()
			}
			c.Add(pages[call%len(pages)], nil, nil)
			call++
		}
	})
	b.Run("known", func(b *testing.B) {
		c := bigCatalog()
		page := slices.Clone(c.Songs[100:120])
		b.ReportAllocs()
		for b.Loop() {
			c.Add(page, nil, nil)
		}
	})
	b.Run("feed_playlists", func(b *testing.B) {
		c := bigCatalog()
		mixes := placeholderMixes()
		for i := range mixes {
			mixes[i].Songs = nil
		}
		b.ReportAllocs()
		for b.Loop() {
			c.Add(nil, nil, mixes)
		}
	})
}

func BenchmarkAddAlbum(b *testing.B) {
	c := bigCatalog()
	album, tracks := benchAlbum()
	b.ReportAllocs()
	for b.Loop() {
		c.AddAlbum(album, tracks)
	}
}

// searchCases search the placeholder and a large library for a hit, a miss
// and a blank query. A miss or a blank query allocates nothing today.
var searchCases = []struct {
	name, query string
	cat         func() *Catalog
	limit       float64
}{
	{"18/hit", "lumen", Placeholder, 4}, // 3 allocs today: the results growing.
	{"18/miss", "zzz", Placeholder, 0},
	{"18/blank", "  ", Placeholder, 0},
	{"5000/hit", "lumen", bigCatalog, 13}, // 11 allocs today.
	{"5000/miss", "zzz", bigCatalog, 0},
	{"5000/blank", "  ", bigCatalog, 0},
}

// BenchmarkSearch measures the placeholder's search without its delay.
func BenchmarkSearch(b *testing.B) {
	for _, tc := range searchCases {
		b.Run(tc.name, func(b *testing.B) {
			src := newPlaceholderSource(tc.cat(), 0)
			b.ReportAllocs()
			for b.Loop() {
				sinkResults = src.search(tc.query)
			}
		})
	}
}

func BenchmarkSong(b *testing.B) {
	c := bigCatalog()
	for _, tc := range []struct{ name, id string }{{"hit", "ember-4000"}, {"miss", "nope"}} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkSong, sinkBool = c.Song(tc.id)
			}
		})
	}
}

func BenchmarkSongsByID(b *testing.B) {
	c := bigCatalog()
	for _, count := range []int{10, 2000} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			ids := c.SongIDs()[:count]
			b.ReportAllocs()
			for b.Loop() {
				sinkSongs = c.SongsByID(ids)
			}
		})
	}
}

func BenchmarkSongIDs(b *testing.B) {
	c := bigCatalog()
	b.ReportAllocs()
	for b.Loop() {
		sinkIDs = c.SongIDs()
	}
}

// BenchmarkAlbumTracks covers a fetched album's track order and, for one
// only seen in a feed, catalog order, which scans every song.
func BenchmarkAlbumTracks(b *testing.B) {
	c := bigCatalog()
	for _, tc := range []struct{ name, id string }{{"track_order", "MPREb_bench"}, {"catalog_order", "neon-tide"}} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkSongs = c.AlbumTracks(tc.id)
			}
		})
	}
}

func BenchmarkAlbum(b *testing.B) {
	c := bigCatalog()
	b.ReportAllocs()
	for b.Loop() {
		sinkAlbum, sinkBool = c.Album("northbound")
	}
}

func BenchmarkPlaylist(b *testing.B) {
	c := bigCatalog()
	b.ReportAllocs()
	for b.Loop() {
		sinkPlaylist, sinkBool = c.Playlist("deep-focus")
	}
}

func BenchmarkLibraryPlaylists(b *testing.B) {
	c := bigCatalog()
	b.ReportAllocs()
	for b.Loop() {
		sinkPlaylists = c.LibraryPlaylists()
	}
}

func BenchmarkAlbumArtists(b *testing.B) {
	albums := Placeholder().Albums
	b.ReportAllocs()
	for b.Loop() {
		sinkArtists = AlbumArtists(albums)
	}
}

func BenchmarkSetLibrary(b *testing.B) {
	c := bigCatalog()
	ids := slices.Clone(c.libraryIDs)
	b.ReportAllocs()
	for b.Loop() {
		c.SetLibrary(ids)
	}
}

func BenchmarkTotalSeconds(b *testing.B) {
	for _, count := range []int{18, 2000} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			songs := scaledSongs(count)
			b.ReportAllocs()
			for b.Loop() {
				sinkInt = TotalSeconds(songs)
			}
		})
	}
}

type sourceCall struct {
	name  string
	limit float64
	call  func()
}

// placeholderCalls calls each method of src, which should have no delay.
func placeholderCalls(ctx context.Context, src *PlaceholderSource) []sourceCall {
	return []sourceCall{
		{"Search", 4, func() { sinkResults, sinkErr = src.Search(ctx, "lumen") }}, // 3 allocs today.
		// 2 allocs today: the album's tracks.
		{"Album", 2, func() { sinkAlbum, sinkSongs, sinkErr = src.Album(ctx, "neon-tide") }},
		// 2 allocs today: the IDs, and the songs.
		{"Playlist", 2, func() { sinkPlaylist, sinkSongs, sinkErr = src.Playlist(ctx, "late-night-drive") }},
		// 2 allocs today, as Playlist.
		{"Playlist/liked", 2, func() { sinkPlaylist, sinkSongs, sinkErr = src.Playlist(ctx, LikedID) }},
		{"Home", 10, func() { sinkHome, sinkErr = src.Home(ctx) }},          // 9 allocs today: shelves of items.
		{"Explore", 4, func() { sinkExplore, sinkErr = src.Explore(ctx) }},  // 3 allocs today.
		{"Account", 0, func() { sinkAccount, sinkErr = src.Account(ctx) }},  // 0 allocs today, as Like.
		{"Library", 13, func() { sinkLibrary, sinkErr = src.Library(ctx) }}, // 11 allocs today: every list copied.
		{"Like", 0, func() { sinkErr = src.Like(ctx, "ember", true) }},
	}
}

func BenchmarkPlaceholderSource(b *testing.B) {
	for _, tc := range placeholderCalls(b.Context(), NewPlaceholderSource(0)) {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				tc.call()
			}
		})
	}
}

// BenchmarkShareStrings is an album's tracks as decoded, and a large
// library's songs.
func BenchmarkShareStrings(b *testing.B) {
	for _, tc := range []struct {
		name  string
		songs []Song
	}{
		{"album", decodedTracks()},
		{strconv.Itoa(bigSongs), scaledSongs(bigSongs)},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				ShareStrings(tc.songs)
			}
		})
	}
}

// BenchmarkShareIDs is a large playlist's IDs as decoded, each a copy of
// its own, reset every run, and its songs.
func BenchmarkShareIDs(b *testing.B) {
	songs, decoded := decodedIDs(bigSongs)
	ids := make([]string, len(decoded))
	b.Run(strconv.Itoa(bigSongs), func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			copy(ids, decoded)
			ShareIDs(ids, songs)
		}
	})
}

// decodedIDs returns n songs and their IDs, each a copy of its own.
func decodedIDs(n int) ([]Song, []string) {
	songs := scaledSongs(n)
	ids := make([]string, len(songs))
	for i, song := range songs {
		ids[i] = strings.Clone(song.ID)
	}
	return songs, ids
}

// BenchmarkKeepSongs is a daemon's queue catalog after a song left the
// queue: all songs scanned, one dropped, the index made again.
func BenchmarkKeepSongs(b *testing.B) {
	songs := scaledSongs(bigSongs)
	drop := songs[bigSongs/2].ID
	keep := func(id string) bool { return id != drop }
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		c := New(slices.Clone(songs), nil, nil)
		b.StartTimer()
		c.KeepSongs(keep)
	}
}
