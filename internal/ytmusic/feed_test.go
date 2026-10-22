package ytmusic

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"yt-music-player/internal/catalog"
)

func TestHome(t *testing.T) {
	var mu sync.Mutex
	var visitors []string
	fixtures := fixtureHandler(t)
	c := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		visitors = append(visitors, r.Header.Get("X-Goog-Visitor-Id"))
		mu.Unlock()
		fixtures(w, r)
	})
	home, err := c.Home(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	// The first page and both continuations; the last has no token.
	visitor := decoded(t, "home.json").ResponseContext.VisitorData
	if want := []string{"", visitor, visitor}; visitor == "" || !slices.Equal(visitors, want) {
		t.Errorf("visitor IDs sent = %q, want %q", visitors, want)
	}

	type shelf struct {
		title string
		kind  catalog.ItemKind
		items int
	}
	want := []shelf{
		{"Featured playlists for you", catalog.ItemPlaylist, 5},
		{"New releases", catalog.ItemAlbum, 10},
		{"Today's fresh & popular", catalog.ItemPlaylist, 9},
		{"Your fav decades, on repeat!", catalog.ItemPlaylist, 10},
		{"Quick picks", catalog.ItemSong, 20},
		{"Trending songs for you", catalog.ItemSong, 9},
	}
	if len(home.Shelves) != len(want) {
		t.Fatalf("got %d shelves, want %d", len(home.Shelves), len(want))
	}
	for i, s := range home.Shelves {
		w := want[i]
		if s.Title != w.title || s.Strapline != "" || len(s.Items) != w.items {
			t.Errorf("shelf %d = %q (strapline %q) with %d items, want %q with %d", i, s.Title, s.Strapline,
				len(s.Items), w.title, w.items)
		}
		for j, it := range s.Items {
			if it.Kind != w.kind || !complete(it) {
				t.Errorf("shelf %q item %d = %+v, want a complete item of kind %d", s.Title, j, it, w.kind)
			}
		}
	}

	chart := home.Shelves[0].Items[0].Playlist
	if chart.ID != chartPlaylistID || chart.Name != "Trending 20 Taiwan" || chart.Owner != "YouTube Charts" ||
		chart.Songs != nil {
		t.Errorf("first playlist = %+v, want Trending 20 Taiwan by YouTube Charts, without VL or songs", chart)
	}
	mix := home.Shelves[2].Items[0].Playlist
	if mix.ID != "RDCLAK5uy_lBNUteBRencHzKelu5iDHwLF6mYqjL-JU" || mix.Name != "Pop Certified" ||
		mix.Owner != "Olivia Rodrigo, Taylor Swift, Sabrina Carpenter, Ariana Grande" {
		t.Errorf("first mix = %+v, want Pop Certified with its artists", mix)
	}
	ep := withoutAlbumArt(home.Shelves[1].Items[6].Album)
	if want := (catalog.Album{ID: "MPREb_ods9q7HQQGq", Title: "27", Artist: "YUQI", Kind: catalog.ReleaseEP}); ep != want {
		t.Errorf("seventh new release = %+v, want %+v", ep, want)
	}
	song := withoutArt(home.Shelves[4].Items[0].Song)
	if want := (catalog.Song{
		ID: "nyNm-dVBMoo", Title: "Ain't In LA", Artist: "ADÉLA", Album: "PRIMA", AlbumID: "MPREb_BtXfAQVI3yJ",
	}); song != want {
		t.Errorf("first quick pick = %+v, want %+v", song, want)
	}
	if got := home.Shelves[5].Items[3].Song.Artist; got != "BigXthaPlug, Ava Max" {
		t.Errorf("artists of the fourth trending song = %q", got)
	}
}

// complete reports whether an item has what the UI shows for its kind.
func complete(it catalog.Item) bool {
	switch it.Kind {
	case catalog.ItemSong:
		s := it.Song
		return s.ID != "" && s.Title != "" && s.Artist != "" && s.Art != ""
	case catalog.ItemAlbum:
		al := it.Album
		return strings.HasPrefix(al.ID, "MPREb_") && al.Title != "" && al.Artist != "" && al.Kind != "" && al.Art != ""
	case catalog.ItemPlaylist:
		pl := it.Playlist
		return pl.ID != "" && !strings.HasPrefix(pl.ID, "VL") && pl.Name != "" && pl.Owner != "" && pl.Art != ""
	case catalog.ItemArtist:
		a := it.Artist
		return strings.HasPrefix(a.ID, "UC") && a.Name != "" && a.Art != ""
	}
	return false
}

// TestHomeStops: continuations stop at the cap or a repeated token, and
// a failed one ends the feed without failing it.
func TestHomeStops(t *testing.T) {
	shelf := `{"musicCarouselShelfRenderer":` +
		`{"header":{"musicCarouselShelfBasicHeaderRenderer":{"title":{"runs":[{"text":"S%d"}]}}},` +
		`"contents":[{"musicTwoRowItemRenderer":{"title":{"runs":[{"text":"A"}]},` +
		`"navigationEndpoint":{"browseEndpoint":{"browseId":"MPREb_%d"}}}}]}}`
	for _, tc := range []struct {
		name        string
		token       func(n int) string
		failAfter   int
		wantShelves int
	}{
		{"endless", strconv.Itoa, 0, maxHomeContinuations + 1},
		{"repeating", func(int) string { return "same" }, 0, 2},
		{"failing", strconv.Itoa, 2, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			c := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
				_, body := readRequest(t, r)
				calls := int(requests.Add(1))
				if tc.failAfter > 0 && calls > tc.failAfter {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				list := fmt.Sprintf(`{"contents":[%s],"continuations":[{"nextContinuationData":{"continuation":%q}}]}`,
					fmt.Sprintf(shelf, calls, calls), tc.token(calls))
				if body["browseId"] == "FEmusic_home" {
					_, _ = fmt.Fprintf(w, `{"responseContext":{"visitorData":"v"},`+
						`"contents":{"singleColumnBrowseResultsRenderer":`+
						`{"tabs":[{"tabRenderer":{"content":{"sectionListRenderer":%s}}}]}}}`, list)
					return
				}
				_, _ = fmt.Fprintf(w, `{"continuationContents":{"sectionListContinuation":%s}}`, list)
			})
			home, err := c.Home(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(home.Shelves) != tc.wantShelves {
				t.Errorf("got %d shelves in %d calls, want %d", len(home.Shelves), requests.Load(), tc.wantShelves)
			}
		})
	}
}

func TestHomeFails(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"status":     func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) },
		"no shelves": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"contents":{}}`)) },
	} {
		if _, err := serverClient(t, h).Home(t.Context()); err == nil {
			t.Errorf("%s: Home succeeded", name)
		}
	}
}

func TestExplore(t *testing.T) {
	ex, err := fixtureClient(t).Explore(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(ex.NewReleases) != 24 || len(ex.Trending) != 20 || len(ex.Moods) != 28 {
		t.Fatalf("got %d new releases, %d trending and %d moods, want 24, 20 and 28",
			len(ex.NewReleases), len(ex.Trending), len(ex.Moods))
	}
	first := catalog.Album{
		ID:     "MPREb_0A7WM2d0SJl",
		Title:  "自由的你 - Free to Be You",
		Artist: "G.E.M.",
		Kind:   catalog.ReleaseSingle,
	}
	if got := withoutAlbumArt(ex.NewReleases[0]); got != first {
		t.Errorf("first new release = %+v, want %+v", got, first)
	}
	kinds := map[catalog.Release]int{}
	for _, al := range ex.NewReleases {
		kinds[al.Kind]++
		if !complete(catalog.Item{Kind: catalog.ItemAlbum, Album: al}) {
			t.Errorf("incomplete new release %+v", al)
		}
	}
	if kinds[catalog.ReleaseSingle] != 18 || kinds[catalog.ReleaseAlbum] != 6 {
		t.Errorf("new releases by kind = %v, want 18 singles and 6 albums", kinds)
	}
	if got := ex.NewReleases[9].Artist; got != "Rubikdice, Chilx, WAA" {
		t.Errorf("artists of the tenth new release = %q", got)
	}

	checkTrending(t, ex.Trending)
	if s := ex.Trending[2]; s.Album != s.Title || s.AlbumID != "MPREb_IfGu7Gt2q8z" {
		t.Errorf("third trending song = %+v, want its single", s)
	}

	chill := catalog.Mood{Name: "Chill", ID: "ggMPOg1uX1JOQWZFeDByc2Jm"}
	if got := ex.Moods[0]; got.Name != chill.Name || got.ID != chill.ID || !near(got.Tint.H, 261.5, 0.1) {
		t.Errorf("first mood = %+v, want Chill, blue", got)
	}
	// Genres follow moods and moments.
	if got := ex.Moods[12].Name; got != "African" {
		t.Errorf("thirteenth mood = %q, want the first genre, African", got)
	}
	for _, m := range ex.Moods {
		if m.Name == "" || !strings.HasPrefix(m.ID, "ggMPOg") || m.Tint.L < minTintL || m.Tint.L > maxTintL ||
			m.Tint.C < 0 || m.Tint.C > maxTintC || m.Tint.H < 0 || m.Tint.H >= 360 {
			t.Errorf("mood %+v out of range", m)
		}
	}
}

// checkTrending checks the chart that explore.json and chart_playlist.json
// both hold, in rank order.
func checkTrending(t *testing.T, songs []catalog.Song) {
	t.Helper()
	ids := make([]string, 0, len(songs))
	for _, s := range songs {
		ids = append(ids, s.ID)
		if s.ID == "" || s.Title == "" || s.Artist == "" || s.Art == "" {
			t.Errorf("incomplete trending song %+v", s)
		}
	}
	want := []string{"0GZ4gQQE1yM", "TBmE7m0yEwY", "R48yZyjztBg", "PL9o0xj7RyM"}
	if len(ids) != 20 || !slices.Equal(ids[:len(want)], want) || ids[19] != "GBRzoOeHV9c" {
		t.Errorf("trending = %q, want 20 starting %q", ids, want)
	}
}

// exploreServer serves the fixtures, but answers the browse IDs in fail
// with a 500, and serves explore.json without its trending shelf if
// noTrending; it reports the browse IDs asked.
func exploreServer(t *testing.T, noTrending bool, fail ...string) (*Client, func() []string) {
	t.Helper()
	fixtures := fixtureHandler(t)
	var mu sync.Mutex
	var asked []string
	c := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		r.Body = io.NopCloser(bytes.NewReader(data))
		var body map[string]any
		_ = json.Unmarshal(data, &body)
		id, _ := body["browseId"].(string)
		mu.Lock()
		asked = append(asked, id)
		mu.Unlock()
		switch {
		case slices.Contains(fail, id):
			w.WriteHeader(http.StatusInternalServerError)
		case id == "FEmusic_explore" && noTrending:
			var page map[string]any
			readFixture(t, "explore.json", &page)
			list := nav(page, "contents", "singleColumnBrowseResultsRenderer", "tabs", 0, "tabRenderer", "content",
				"sectionListRenderer").(map[string]any)
			list["contents"] = slices.DeleteFunc(list["contents"].([]any), func(sec any) bool {
				id, _ := nav(sec, "musicCarouselShelfRenderer", "header", "musicCarouselShelfBasicHeaderRenderer",
					"title", "runs", 0, "navigationEndpoint", "browseEndpoint", "browseId").(string)
				return strings.HasPrefix(id, "VL")
			})
			_ = json.MarshalWrite(w, page)
		default:
			fixtures(w, r)
		}
	})
	return c, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Sorted(slices.Values(asked))
	}
}

func TestExploreAsksNoChartsWhenTrending(t *testing.T) {
	c, asked := exploreServer(t, false)
	if _, err := c.Explore(t.Context()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"FEmusic_explore", "FEmusic_moods_and_genres"}; !slices.Equal(asked(), want) {
		t.Errorf("asked %q, want %q", asked(), want)
	}
}

func TestExploreChartsFallback(t *testing.T) {
	c, asked := exploreServer(t, true)
	ex, err := c.Explore(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(ex.NewReleases) != 24 || len(ex.Moods) != 28 {
		t.Errorf("got %d new releases and %d moods, want 24 and 28", len(ex.NewReleases), len(ex.Moods))
	}
	checkTrending(t, ex.Trending)
	// The playlist has durations, which explore's chart rows lack.
	if ex.Trending[0].Seconds != 298 {
		t.Errorf("first song = %+v, want the chart playlist's, 298 s long", ex.Trending[0])
	}
	want := []string{"FEmusic_charts", "FEmusic_explore", "FEmusic_moods_and_genres", "VL" + chartPlaylistID}
	if !slices.Equal(asked(), want) {
		t.Errorf("asked %q, want %q", asked(), want)
	}
}

func TestExplorePartialFailure(t *testing.T) {
	for _, tc := range []struct {
		fail                      []string
		noTrending                bool
		newReleases, trend, moods int
	}{
		{fail: []string{"FEmusic_moods_and_genres"}, newReleases: 24, trend: 20},
		{fail: []string{"FEmusic_explore"}, trend: 20, moods: 28},
		{fail: []string{"FEmusic_explore", "FEmusic_charts"}, moods: 28},
		{fail: []string{"FEmusic_charts"}, noTrending: true, newReleases: 24, moods: 28},
		{fail: []string{"VL" + chartPlaylistID, "FEmusic_moods_and_genres"}, noTrending: true, newReleases: 24},
	} {
		c, _ := exploreServer(t, tc.noTrending, tc.fail...)
		ex, err := c.Explore(t.Context())
		isExpected := len(ex.NewReleases) == tc.newReleases && len(ex.Trending) == tc.trend && len(ex.Moods) == tc.moods
		if err != nil || !isExpected {
			t.Errorf("failing %q: got %d new releases, %d trending, %d moods (err %v), want %d, %d, %d",
				tc.fail, len(ex.NewReleases), len(ex.Trending), len(ex.Moods), err, tc.newReleases, tc.trend, tc.moods)
		}
	}
}

func TestExploreFailsWhenEveryPartFails(t *testing.T) {
	c, _ := exploreServer(t, false, "FEmusic_explore", "FEmusic_charts", "FEmusic_moods_and_genres")
	ex, err := c.Explore(t.Context())
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("Explore = %+v, %v; want an error with the causes", ex, err)
	}
}

// TestChartsArtists reads charts.json's top artists, the fixtures' only
// artist rows.
func TestChartsArtists(t *testing.T) {
	shelves := parseShelves(sectionList(decoded(t, "charts.json")).sections())
	if len(shelves) != 2 || shelves[0].Title != "Video charts" || shelves[1].Title != "Top artists" {
		t.Fatalf("shelves = %+v", shelves)
	}
	for _, it := range shelves[1].Items {
		if it.Kind != catalog.ItemArtist || !complete(it) {
			t.Errorf("top artist %+v", it)
		}
	}
	if a := shelves[1].Items[0].Artist; a.ID != "UCL2MDNdwEtV6aYUgNjFQGZA" || a.Name != "Jay Chou" {
		t.Errorf("first artist = %+v, want Jay Chou", a)
	}
}

func TestParseShelf(t *testing.T) {
	const items = `[
		{"musicTwoRowItemRenderer":{"title":{"runs":[{"text":"One More Time"}]},
			"subtitle":{"runs":[{"text":"Song"},{"text":" • "},{"text":"Daft Punk","navigationEndpoint":{"browseEndpoint":{"browseId":"UCx","browseEndpointContextSupportedConfigs":{"browseEndpointContextMusicConfig":{"pageType":"MUSIC_PAGE_TYPE_ARTIST"}}}}},{"text":" • "},{"text":"Discovery","navigationEndpoint":{"browseEndpoint":{"browseId":"MPREb_d"}}}]},
			"navigationEndpoint":{"watchEndpoint":{"videoId":"FGBhQbmPwH8"}},
			"thumbnailRenderer":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"s","width":60,"height":60},{"url":"l","width":544,"height":544}]}}}}},
		{"musicTwoRowItemRenderer":{"title":{"runs":[{"text":"Daft Punk"}]},"subtitle":{"runs":[{"text":"2.1M subscribers"}]},
			"navigationEndpoint":{"browseEndpoint":{"browseId":"UCRr1xG_2WIDs18a6cIiCxeA","browseEndpointContextSupportedConfigs":{"browseEndpointContextMusicConfig":{"pageType":"MUSIC_PAGE_TYPE_ARTIST"}}}}}},
		{"musicTwoRowItemRenderer":{"title":{"runs":[{"text":"Supermix"}]},"subtitle":{"runs":[{"text":"Mix"},{"text":" • "},{"text":"Daft Punk, Justice"}]},
			"navigationEndpoint":{"watchPlaylistEndpoint":{"playlistId":"RDTMAK5uy_x"}}}},
		{"musicTwoRowItemRenderer":{"title":{"runs":[{"text":"Radio"}]},"navigationEndpoint":{"watchPlaylistEndpoint":{"playlistId":"RDAMVMx"}}}},
		{"musicTwoRowItemRenderer":{"title":{"runs":[{"text":"A podcast"}]},
			"navigationEndpoint":{"browseEndpoint":{"browseId":"MPSPx","browseEndpointContextSupportedConfigs":{"browseEndpointContextMusicConfig":{"pageType":"MUSIC_PAGE_TYPE_PODCAST_SHOW_DETAIL_PAGE"}}}}}},
		{"musicTwoRowItemRenderer":{"title":{"runs":[{"text":"A channel"}]},
			"navigationEndpoint":{"browseEndpoint":{"browseId":"UCy","browseEndpointContextSupportedConfigs":{"browseEndpointContextMusicConfig":{"pageType":"MUSIC_PAGE_TYPE_USER_CHANNEL"}}}}}},
		{"musicTwoRowItemRenderer":{"title":{"runs":[{"text":"Untitled has no page"}]}}},
		{"musicMultiRowListItemRenderer":{"title":{"runs":[{"text":"An episode"}]}}},
		{"musicResponsiveListItemRenderer":{"flexColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Homework"}]}}},
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Album"},{"text":" • "},{"text":"Daft Punk"},{"text":" • "},{"text":"1997"}]}}}],
			"navigationEndpoint":{"browseEndpoint":{"browseId":"MPREb_h"}}}},
		{"musicResponsiveListItemRenderer":{"flexColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Mix"}]}}}],
			"navigationEndpoint":{"browseEndpoint":{"browseId":"VLRDCLAK5uy_z"}}}}
	]`
	const header = `{"musicCarouselShelfBasicHeaderRenderer":{"strapline":{"runs":[{"text":"More from"}]}}}`
	var sh shelf
	if err := json.Unmarshal([]byte(`{"header":`+header+`,"contents":`+items+`}`), &sh); err != nil {
		t.Fatal(err)
	}
	s, ok := parseShelf(&sh)
	if !ok || s.Title != "More from" || s.Strapline != "" {
		t.Fatalf("shelf = %q (strapline %q), %v; want the strapline as title", s.Title, s.Strapline, ok)
	}
	want := []catalog.Item{
		{Kind: catalog.ItemSong, Song: catalog.Song{
			ID: "FGBhQbmPwH8", Title: "One More Time", Artist: "Daft Punk", Album: "Discovery", AlbumID: "MPREb_d",
			Art: "l",
		}},
		{Kind: catalog.ItemArtist, Artist: catalog.Artist{ID: "UCRr1xG_2WIDs18a6cIiCxeA", Name: "Daft Punk"}},
		{Kind: catalog.ItemPlaylist, Playlist: catalog.Playlist{
			ID: "RDTMAK5uy_x", Name: "Supermix", Owner: "Daft Punk, Justice",
		}},
		{Kind: catalog.ItemAlbum, Album: catalog.Album{
			ID: "MPREb_h", Title: "Homework", Artist: "Daft Punk", Year: 1997, Kind: catalog.ReleaseAlbum,
		}},
		{Kind: catalog.ItemPlaylist, Playlist: catalog.Playlist{ID: "RDCLAK5uy_z", Name: "Mix"}},
	}
	if len(s.Items) != len(want) {
		t.Fatalf("got %d items, want %d: %+v", len(s.Items), len(want), s.Items)
	}
	for i, it := range s.Items {
		w := want[i]
		isSamePlaylist := it.Playlist.ID == w.Playlist.ID && it.Playlist.Name == w.Playlist.Name &&
			it.Playlist.Owner == w.Playlist.Owner
		if it.Kind != w.Kind || it.Song != w.Song || it.Album != w.Album || it.Artist != w.Artist || !isSamePlaylist {
			t.Errorf("item %d = %+v, want %+v", i, it, w)
		}
	}

	sh = shelf{}
	if err := json.Unmarshal([]byte(`{"header":{},"contents":[{"musicTwoRowItemRenderer":{}}]}`), &sh); err != nil {
		t.Fatal(err)
	}
	if _, ok := parseShelf(&sh); ok {
		t.Error("a shelf without items was kept")
	}
}

func TestRanked(t *testing.T) {
	row := func(id, rank string) string {
		return fmt.Sprintf(`{"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":%q},`+
			`"flexColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"t"}]}}}],`+
			`"customIndexColumn":{"musicCustomIndexColumnRenderer":{"text":{"runs":[{"text":%q}]}}}}}`, id, rank)
	}
	var rows []item
	rowsJSON := []string{row("c", "3"), row("x", ""), row("a", "1"), row("y", "-"), row("b", "2")}
	data := "[" + strings.Join(rowsJSON, ",") + "]"
	if err := json.Unmarshal([]byte(data), &rows); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range ranked(rows) {
		ids = append(ids, s.ID)
	}
	if want := []string{"a", "b", "c", "x", "y"}; !slices.Equal(ids, want) {
		t.Errorf("ranked = %q, want %q", ids, want)
	}
}

func TestOklch(t *testing.T) {
	for _, tc := range []struct {
		argb    uint32
		l, c, h float64
	}{
		{0xffffffff, 1, 0, 0},
		{0xff000000, 0, 0, 0},
		{0x00808080, 0.5999, 0, 0}, // Alpha is ignored.
		{0xffff0000, 0.6280, 0.2577, 29.23},
		{0xff00ff00, 0.8664, 0.2948, 142.50},
		{0xff0000ff, 0.4520, 0.3132, 264.05},
		{0xffff00ff, 0.7017, 0.3225, 328.36},
	} {
		l, c, h := oklch(tc.argb)
		if math.Abs(l-tc.l) > 1e-3 || math.Abs(c-tc.c) > 1e-3 || math.Abs(h-tc.h) > 0.05 {
			t.Errorf("oklch(%#08x) = %.4f %.4f %.2f, want %.4f %.4f %.2f", tc.argb, l, c, h, tc.l, tc.c, tc.h)
		}
	}
}

func TestTintClamps(t *testing.T) {
	for _, tc := range []struct {
		argb uint32
		want catalog.Tint
	}{
		{0xff000000, catalog.Tint{L: minTintL}},
		{0xffffffff, catalog.Tint{L: maxTintL}},
		{0xff0000ff, catalog.Tint{L: minTintL, C: maxTintC, H: 264.05}},
		{0xff7fa8ff, catalog.Tint{L: 0.7383, C: 0.1344, H: 264.2}}, // In range: kept.
	} {
		got := tint(tc.argb)
		if !near(got.L, tc.want.L, 1e-3) || !near(got.C, tc.want.C, 1e-3) || !near(got.H, tc.want.H, 0.1) {
			t.Errorf("tint(%#08x) = %+v, want %+v", tc.argb, got, tc.want)
		}
	}
}

func near(a, b, tolerance float32) bool { return math.Abs(float64(a-b)) <= float64(tolerance) }

func readFixture(t *testing.T, name string, v any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatal(err)
	}
}
