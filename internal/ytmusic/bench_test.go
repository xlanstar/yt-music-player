package ytmusic

import (
	"bytes"
	"cmp"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/innertube"
)

// Sinks keep the compiler from optimizing measured calls away.
var (
	sinkResponse *response
	sinkShelf    *shelf
	sinkRows     []item
	sinkString   string
	sinkInt      int
	sinkBool     bool
	sinkErr      error
	sinkSong     catalog.Song
	sinkSongs    []catalog.Song
	sinkAlbum    catalog.Album
	sinkAlbums   []catalog.Album
	sinkPlaylist catalog.Playlist
	sinkShelves  []catalog.Shelf
	sinkItem     catalog.Item
	sinkMoods    []catalog.Mood
	sinkAccount  catalog.Account
	sinkArt      catalog.Art
	sinkTint     catalog.Tint
	sinkFloat    float64
	sinkRelease  catalog.Release
	sinkResults  catalog.Results
	sinkHome     catalog.Home
	sinkExplore  catalog.Explore
)

func readBytes(tb testing.TB, name string) []byte {
	tb.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		tb.Fatal(err)
	}
	return data
}

// decoded is a fixture as callWith hands it to the parsers.
func decoded(tb testing.TB, name string) *response {
	tb.Helper()
	v, err := decode(bytes.NewReader(readBytes(tb, name)))
	if err != nil {
		tb.Fatal(err)
	}
	return v
}

// songRows are the song rows of a page, without its continuation item.
func songRows(rows []item) []item {
	var out []item
	for _, r := range rows {
		if r.ListItem != nil {
			out = append(out, r)
		}
	}
	return out
}

// scaledRows repeats rows to n, the size of a long playlist.
func scaledRows(rows []item, n int) []item {
	out := make([]item, n)
	for i := range out {
		out[i] = rows[i%len(rows)]
	}
	return out
}

func playlistRows(tb testing.TB) []item {
	tb.Helper()
	_, rows, ok := playlistPage(playlistID, decoded(tb, "playlist.json"))
	if !ok {
		tb.Fatal("playlist.json has no page")
	}
	return rows
}

// BenchmarkDecode is what callWith pays for a response before any parser
// runs.
func BenchmarkDecode(b *testing.B) {
	for _, name := range []string{"search_songs", "album", "playlist", "home", "explore"} {
		data := readBytes(b, name+".json")
		b.Run(name, func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			var r bytes.Reader
			b.ReportAllocs()
			for b.Loop() {
				r.Reset(data)
				sinkResponse, sinkErr = decode(&r)
			}
		})
	}
}

func BenchmarkSearchSongs(b *testing.B) {
	resp := decoded(b, "search_songs.json")
	b.ReportAllocs()
	for b.Loop() {
		sinkSongs = parseRows(searchRows(resp), parseSong)
	}
}

func BenchmarkSearchAlbums(b *testing.B) {
	resp := decoded(b, "search_albums.json")
	b.ReportAllocs()
	for b.Loop() {
		sinkAlbums = parseRows(searchRows(resp), parseAlbumRow)
	}
}

func BenchmarkAlbumPage(b *testing.B) {
	resp := decoded(b, "album.json")
	b.ReportAllocs()
	for b.Loop() {
		sinkAlbum, sinkSongs, sinkBool = albumPage(albumID, resp)
	}
}

func BenchmarkPlaylistPage(b *testing.B) {
	resp := decoded(b, "playlist.json")
	b.ReportAllocs()
	for b.Loop() {
		sinkPlaylist, sinkRows, sinkBool = playlistPage(playlistID, resp)
	}
}

func BenchmarkPlaylistSongs(b *testing.B) {
	first := playlistRows(b)
	_, chart, _ := playlistPage(chartPlaylistID, decoded(b, "chart_playlist.json"))
	for _, tc := range []struct {
		name string
		rows []item
	}{
		{"first_page", first},
		{"continuation", continuationItems(decoded(b, "playlist_continuation.json"))},
		{"chart", chart},
		{"2000_songs", scaledRows(songRows(first), 2000)},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkSongs = playlistSongs(nil, tc.rows)
			}
		})
	}
}

func BenchmarkContinuation(b *testing.B) {
	rows := playlistRows(b)
	b.ReportAllocs()
	for b.Loop() {
		sinkString = continuation(rows)
	}
}

// BenchmarkParseSong is one song row, cycling through a page's.
func BenchmarkParseSong(b *testing.B) {
	for _, tc := range []struct{ name, file string }{
		{"search", "search_songs.json"},
		{"playlist", "playlist.json"},
		{"library", "signed_in/library_songs.json"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			resp := decoded(b, tc.file)
			var rows []item
			switch tc.name {
			case "search":
				rows = searchRows(resp)
			case "playlist":
				_, rows, _ = playlistPage(playlistID, resp)
			default:
				rows = libraryContents(resp).rows()
			}
			rows = songRows(rows)
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				sinkSong, sinkBool = parseSong(rows[i%len(rows)])
				i++
			}
		})
	}
}

func BenchmarkParseAlbumRow(b *testing.B) {
	rows := searchRows(decoded(b, "search_albums.json"))
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		sinkAlbum, sinkBool = parseAlbumRow(rows[i%len(rows)])
		i++
	}
}

func BenchmarkParseShelves(b *testing.B) {
	for _, tc := range []struct{ name, file string }{
		{"home", "home.json"},
		{"continuation_1", "home_continuation_1.json"},
		{"continuation_2", "home_continuation_2.json"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			resp := decoded(b, tc.file)
			list := cmp.Or(sectionList(resp), resp.ContinuationContents.SectionList)
			sections := list.sections()
			if len(sections) == 0 {
				b.Fatal("no sections")
			}
			b.ReportAllocs()
			for b.Loop() {
				sinkShelves = parseShelves(sections)
			}
		})
	}
}

// BenchmarkParseItem is one library row, cycling through a part's, as
// Library parses them.
func BenchmarkParseItem(b *testing.B) {
	for _, part := range []string{"liked_playlists", "liked_albums", "library_artists"} {
		b.Run(part, func(b *testing.B) {
			rows := libraryContents(decoded(b, "signed_in/"+part+".json")).rows()
			if len(rows) == 0 {
				b.Fatal("no rows")
			}
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				sinkItem, sinkBool = parseItem(rows[i%len(rows)])
				i++
			}
		})
	}
}

func BenchmarkLibraryContents(b *testing.B) {
	resp := decoded(b, "signed_in/library_songs.json")
	b.ReportAllocs()
	for b.Loop() {
		sinkShelf = libraryContents(resp)
	}
}

func BenchmarkExplorePage(b *testing.B) {
	resp := decoded(b, "explore.json")
	b.ReportAllocs()
	for b.Loop() {
		sinkAlbums, sinkSongs = explorePage(resp)
	}
}

func BenchmarkMoodsPage(b *testing.B) {
	resp := decoded(b, "moods_and_genres.json")
	b.ReportAllocs()
	for b.Loop() {
		sinkMoods = moodsPage(resp)
	}
}

func BenchmarkChartPage(b *testing.B) {
	resp := decoded(b, "charts.json")
	b.ReportAllocs()
	for b.Loop() {
		sinkSongs, sinkString = chartPage(resp)
	}
}

func BenchmarkAccountMenu(b *testing.B) {
	resp := decoded(b, "signed_in/account_menu.json")
	b.ReportAllocs()
	for b.Loop() {
		sinkAccount, sinkBool = accountMenu(resp)
	}
}

// BenchmarkSignedOut runs on every signed response.
func BenchmarkSignedOut(b *testing.B) {
	for _, tc := range []struct{ name, file string }{
		{"tracked", "signed_in/liked_music.json"},
		{"sign_in_page", "signed_in/signed_out_page.json"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			resp := decoded(b, tc.file)
			b.ReportAllocs()
			for b.Loop() {
				sinkBool = signedOut(resp)
			}
		})
	}
}

// BenchmarkArt is thumbnail choice: a song row's few thumbnails, and an
// album header's four sizes.
func BenchmarkArt(b *testing.B) {
	for _, tc := range []struct {
		name string
		a    *artwork
	}{
		{"row", &songRows(playlistRows(b))[0].ListItem.artwork},
		{"album_header", &headerColumn(decoded(b, "album.json"))[0].Header.artwork},
	} {
		b.Run(tc.name, func(b *testing.B) {
			if tc.a.art() == "" {
				b.Fatal("no art")
			}
			b.ReportAllocs()
			for b.Loop() {
				sinkArt = tc.a.art()
			}
		})
	}
}

func BenchmarkSeconds(b *testing.B) {
	for _, tc := range []struct{ name, in string }{
		{"m:ss", "3:45"},
		{"h:mm:ss", "1:02:03"},
		{"not_a_duration", "Daft Punk"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkInt = seconds(tc.in)
			}
		})
	}
}

func BenchmarkRunsText(b *testing.B) {
	for _, tc := range []struct {
		name string
		runs runs
	}{
		{"one", songRows(playlistRows(b))[0].ListItem.column(0)},
		{"several", runs{{Text: "Daft Punk"}, {Text: " • "}, {Text: "Discovery"}, {Text: " • "}, {Text: "2001"}}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkString = tc.runs.String()
			}
		})
	}
}

// BenchmarkMetaRead is subtitle-run parsing: a search row's artists,
// album and duration, as parseSong reads them.
func BenchmarkMetaRead(b *testing.B) {
	rows := songRows(searchRows(decoded(b, "search_songs.json")))
	runs := skipLabel(rows[0].ListItem.column(1), typeLabels)
	b.ReportAllocs()
	for b.Loop() {
		var m meta
		m.read(runs, true)
		sinkString = m.artist()
	}
}

func BenchmarkSubtitle(b *testing.B) {
	rows := searchRows(decoded(b, "search_albums.json"))
	runs := rows[0].ListItem.column(1)
	b.ReportAllocs()
	for b.Loop() {
		sinkRelease, sinkString, sinkInt = subtitle(runs)
	}
}

func BenchmarkTint(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		sinkTint = tint(0xff1db954)
	}
}

func BenchmarkOklch(b *testing.B) {
	b.ReportAllocs()
	var l, c, h float64
	for b.Loop() {
		l, c, h = oklch(0xffd7385e)
	}
	sinkFloat = l + c + h
}

// benchClient serves the recorded fixtures from memory, so end-to-end
// benchmarks measure the client, transport and loopback HTTP, not disk.
func benchClient(b *testing.B) *Client {
	b.Helper()
	var index []exchange
	if err := json.Unmarshal(readBytes(b, indexFile), &index); err != nil {
		b.Fatal(err)
	}
	responses := map[string][]byte{}
	for _, ex := range index {
		responses[requestKey(ex.Endpoint, ex.Body)] = readBytes(b, ex.File)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(data, &body)
		delete(body, "context")
		resp, ok := responses[requestKey(r.URL.Path[len("/youtubei/v1/"):], body)]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	b.Cleanup(srv.Close)
	return New(&innertube.Client{HTTP: srv.Client(), BaseURL: srv.URL + "/youtubei/v1/", HL: "en", Retries: -1}, nil)
}

// benchBytes is the size of the responses a call reads, for MB/s.
func benchBytes(b *testing.B, files ...string) int64 {
	b.Helper()
	var n int64
	for _, f := range files {
		n += int64(len(readBytes(b, f)))
	}
	return n
}

func BenchmarkSearch(b *testing.B) {
	c := benchClient(b)
	ctx := b.Context()
	b.SetBytes(benchBytes(b, "search_songs.json", "search_albums.json"))
	b.ReportAllocs()
	for b.Loop() {
		if sinkResults, sinkErr = c.Search(ctx, searchQuery); sinkErr != nil {
			b.Fatal(sinkErr)
		}
	}
}

func BenchmarkAlbum(b *testing.B) {
	c := benchClient(b)
	ctx := b.Context()
	b.SetBytes(benchBytes(b, "album.json"))
	b.ReportAllocs()
	for b.Loop() {
		if sinkAlbum, sinkSongs, sinkErr = c.Album(ctx, albumID); sinkErr != nil {
			b.Fatal(sinkErr)
		}
	}
}

// BenchmarkPlaylist is a page and its one continuation.
func BenchmarkPlaylist(b *testing.B) {
	c := benchClient(b)
	ctx := b.Context()
	b.SetBytes(benchBytes(b, "playlist.json", "playlist_continuation.json"))
	b.ReportAllocs()
	for b.Loop() {
		if sinkPlaylist, sinkSongs, sinkErr = c.Playlist(ctx, playlistID); sinkErr != nil {
			b.Fatal(sinkErr)
		}
	}
}

// BenchmarkHome is the first page and its two recorded continuations.
func BenchmarkHome(b *testing.B) {
	c := benchClient(b)
	ctx := b.Context()
	b.SetBytes(benchBytes(b, "home.json", "home_continuation_1.json", "home_continuation_2.json"))
	b.ReportAllocs()
	for b.Loop() {
		if sinkHome, sinkErr = c.Home(ctx); sinkErr != nil {
			b.Fatal(sinkErr)
		}
	}
}

// BenchmarkExplore is explore and moods, browsed concurrently.
func BenchmarkExplore(b *testing.B) {
	c := benchClient(b)
	ctx := b.Context()
	b.SetBytes(benchBytes(b, "explore.json", "moods_and_genres.json"))
	b.ReportAllocs()
	for b.Loop() {
		if sinkExplore, sinkErr = c.Explore(ctx); sinkErr != nil {
			b.Fatal(sinkErr)
		}
	}
}
