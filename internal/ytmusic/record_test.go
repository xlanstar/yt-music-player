package ytmusic

import (
	"bytes"
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"yt-music-player/internal/auth"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/innertube"
)

var update = flag.Bool("update", false, "record testdata from the live API: go test -run Record -update -v")

// TestRecord is the only test that touches the network, and doubles as
// a live smoke test: it logs what YouTube Music returned. Each subtest
// (-run Record/feeds) records alone and keeps the other fixtures.
func TestRecord(t *testing.T) {
	if !*update {
		t.Skip("records from the live API; run with -update")
	}
	rec := &recorder{next: http.DefaultTransport}
	api := innertube.New()
	api.GL = "US" // Region changes results; pin it so that re-recordings compare.
	api.HTTP = &http.Client{Transport: rec, Timeout: 30 * time.Second}
	c := New(api, nil)

	t.Run("catalog", func(t *testing.T) {
		rec.start("catalog")
		recordCatalog(t, rec, c)
	})
	t.Run("feeds", func(t *testing.T) {
		rec.start("feeds")
		recordFeeds(t, rec, c)
	})
	t.Run("library", recordLibrary)
}

// recordLibrary smoke-tests the signed-in calls live with the account in
// YTM_COOKIE. It writes no fixtures: signed-in responses hold personal
// data no trimming reliably removes, so testdata/signed_in is written by
// hand. It logs only counts and shapes, never names, the user's own IDs
// or the cookie, and never changes the account (no Like).
func recordLibrary(t *testing.T) {
	cookie := os.Getenv("YTM_COOKIE")
	if cookie == "" {
		t.Skip("set YTM_COOKIE to a signed-in music.youtube.com Cookie header")
	}
	ctx := t.Context()
	counter := &countingTransport{next: http.DefaultTransport}
	api := innertube.New()
	api.HTTP = &http.Client{Transport: counter, Timeout: 30 * time.Second}
	c := New(api, &auth.MemoryStore{})
	acct, err := c.SignIn(ctx, cookie)
	if err != nil {
		t.Fatal(err) // Errors never hold the cookie.
	}
	t.Logf("account: name %v, handle %v, photo %v", acct.Name != "", acct.Handle != "", acct.Photo != "")

	// What the library pages look like, for whoever updates the fixtures.
	for _, id := range []string{libraryPlaylists, libraryAlbums, libraryArtists, librarySongs} {
		var data []byte
		err := c.api.Call(ctx, "browse", map[string]any{"browseId": id}, func(r io.Reader) (err error) {
			data, err = io.ReadAll(r)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		resp, err := decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		var raw any
		_ = json.Unmarshal(data, &raw)
		first, _ := nav(raw, "contents", "singleColumnBrowseResultsRenderer", "tabs", 0, "tabRenderer",
			"content", "sectionListRenderer", "contents", 0).(map[string]any)
		var renderer string
		for k := range first {
			renderer = k
		}
		page := libraryContents(resp)
		rows := page.rows()
		paging := "none"
		switch {
		case page.next() != "":
			paging = "nextContinuationData"
		case continuation(rows) != "":
			paging = "continuationItemRenderer"
		}
		var kinds []string
		for _, row := range rows[:min(3, len(rows))] {
			switch {
			case row.ListItem != nil:
				kinds = append(kinds, "musicResponsiveListItemRenderer")
			case row.Card != nil:
				kinds = append(kinds, "musicTwoRowItemRenderer")
			case row.Continuation != nil:
				kinds = append(kinds, "continuationItemRenderer")
			default:
				kinds = append(kinds, "other")
			}
		}
		t.Logf("%s: first section %s, %d rows on page 1 (%v…), paging %s", id, renderer, len(rows), kinds, paging)
		if id == libraryPlaylists {
			var auto []string
			for _, row := range rows {
				it, ok := parseItem(row)
				pid := it.Playlist.ID
				if !ok || strings.HasPrefix(pid, "PL") || strings.HasPrefix(pid, "OLAK") {
					continue
				}
				if len(pid) > 4 {
					pid = pid[:4] + "…"
				}
				auto = append(auto, pid)
			}
			t.Logf("  playlists not made by users (PL…) or albums (OLAK…): %v", auto)
		}
	}

	counter.n.Store(0)
	lib, err := c.Library(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("library: %d playlists, %d albums, %d artists, %d songs in %d requests",
		len(lib.Playlists), len(lib.Albums), len(lib.Artists), len(lib.Songs), counter.n.Load())
	for i, s := range lib.Songs {
		if s.ID == "" || s.Title == "" || s.Seconds == 0 {
			t.Errorf("song %d lacks fields: ID %v, title %v, seconds %v", i, s.ID != "", s.Title != "", s.Seconds != 0)
		}
	}
	for i, a := range lib.Artists {
		if a.ID == "" || a.Name == "" {
			t.Errorf("artist %d lacks fields", i)
		}
	}
	for i, al := range lib.Albums {
		if al.ID == "" || al.Title == "" || al.Artist == "" {
			t.Errorf("album %d lacks fields: title %v, artist %v", i, al.Title != "", al.Artist != "")
		}
	}

	pl, songs, err := c.Playlist(ctx, catalog.LikedID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("liked music: name %q, owner set %v, %d songs", pl.Name, pl.Owner != "", len(songs))
	home, err := c.Home(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("home signed in: %d shelves", len(home.Shelves))
}

// countingTransport counts requests and keeps nothing of them.
type countingTransport struct {
	next http.RoundTripper
	n    atomic.Int32
}

func (c *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.n.Add(1)
	return c.next.RoundTrip(req)
}

func recordCatalog(t *testing.T, rec *recorder, c *Client) {
	ctx := t.Context()
	res, err := c.Search(ctx, searchQuery)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("search %q: %d songs, %d albums", searchQuery, len(res.Songs), len(res.Albums))
	for _, s := range res.Songs[:min(3, len(res.Songs))] {
		t.Logf("  song %+v", s)
	}
	for _, al := range res.Albums[:min(3, len(res.Albums))] {
		t.Logf("  album %+v", al)
	}
	al, tracks, err := c.Album(ctx, albumID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("album %+v: %d tracks, first %+v", al, len(tracks), tracks[0])
	pl, songs, err := c.Playlist(ctx, playlistID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("playlist %q by %q: %d songs, last %+v", pl.Name, pl.Owner, len(songs), songs[len(songs)-1])
	if _, _, err := c.Playlist(ctx, missingPlaylistID); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("missing playlist: err = %v, want ErrNotFound", err)
	}

	rec.write(t)

	// Trimming must keep everything the parsers read.
	fc := fixtureClient(t)
	res2, err := fc.Search(ctx, searchQuery)
	if err != nil || !reflect.DeepEqual(res, res2) {
		t.Errorf("search from fixtures differs (err %v)", err)
	}
	al2, tracks2, err := fc.Album(ctx, albumID)
	if err != nil || al2 != al || !slices.Equal(tracks, tracks2) {
		t.Errorf("album from fixtures differs (err %v)", err)
	}
	pl2, songs2, err := fc.Playlist(ctx, playlistID)
	if err != nil || !reflect.DeepEqual(pl, pl2) || !slices.Equal(songs, songs2) {
		t.Errorf("playlist from fixtures differs (err %v)", err)
	}
}

func recordFeeds(t *testing.T, rec *recorder, c *Client) {
	ctx := t.Context()
	home, err := c.Home(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("home: %d shelves", len(home.Shelves))
	for _, s := range home.Shelves {
		kinds := map[catalog.ItemKind]int{}
		for _, item := range s.Items {
			kinds[item.Kind]++
		}
		t.Logf("  %q (strapline %q): %d items, by kind (song album playlist artist) %v; first %+v",
			s.Title, s.Strapline, len(s.Items), kinds, s.Items[0])
	}
	ex, err := c.Explore(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("explore: %d new releases, %d trending, %d moods", len(ex.NewReleases), len(ex.Trending), len(ex.Moods))
	for _, al := range ex.NewReleases[:min(3, len(ex.NewReleases))] {
		t.Logf("  new %+v", al)
	}
	for i, s := range ex.Trending[:min(3, len(ex.Trending))] {
		t.Logf("  #%d %+v", i+1, s)
	}
	for _, m := range ex.Moods[:min(5, len(ex.Moods))] {
		t.Logf("  mood %+v", m)
	}
	// Explore reaches the charts only when it lacks a chart; record them
	// anyway, so the fallback is tested.
	chart, err := c.chart(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("charts fallback: %d songs, first %+v", len(chart), chart[0])

	rec.write(t)

	fc := fixtureClient(t)
	home2, err := fc.Home(ctx)
	if err != nil || !reflect.DeepEqual(home, home2) {
		t.Errorf("home from fixtures differs (err %v)", err)
	}
	ex2, err := fc.Explore(ctx)
	if err != nil || !reflect.DeepEqual(ex, ex2) {
		t.Errorf("explore from fixtures differs (err %v)", err)
	}
	chart2, err := fc.chart(ctx)
	if err != nil || !slices.Equal(chart, chart2) {
		t.Errorf("charts from fixtures differ (err %v)", err)
	}
}

type recorder struct {
	next http.RoundTripper
	mu   sync.Mutex
	// phase names the subtest recording, which tells apart requests that
	// look alike, like the continuations of a playlist and of home.
	phase         string
	continuations int
	log           []recorded
}

func (r *recorder) start(phase string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.phase, r.continuations, r.log = phase, 0, nil
}

type recorded struct {
	exchange
	response []byte
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	reqBody, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	req.Body = io.NopCloser(bytes.NewReader(reqBody))
	resp, err := r.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	respBody, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(respBody))

	var body map[string]any
	if err := json.Unmarshal(reqBody, &body); err != nil {
		return nil, err
	}
	delete(body, "context")
	endpoint := strings.TrimPrefix(req.URL.Path, "/youtubei/v1/")
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log = append(r.log, recorded{exchange{endpoint, body, r.fixtureName(body)}, respBody})
	return resp, nil
}

func (r *recorder) fixtureName(body map[string]any) string {
	id, _ := body["browseId"].(string)
	switch {
	case body["params"] == paramsSongs:
		return "search_songs.json"
	case body["params"] == paramsAlbums:
		return "search_albums.json"
	case body["continuation"] != nil && r.phase == "feeds":
		r.continuations++
		return fmt.Sprintf("home_continuation_%d.json", r.continuations)
	case body["continuation"] != nil:
		return "playlist_continuation.json"
	case id == "FEmusic_home":
		return "home.json"
	case id == "FEmusic_explore":
		return "explore.json"
	case id == "FEmusic_moods_and_genres":
		return "moods_and_genres.json"
	case id == "FEmusic_charts":
		return "charts.json"
	case id == "VL"+missingPlaylistID:
		return "not_found.json"
	case strings.HasPrefix(id, "MPRE"):
		return "album.json"
	case r.phase == "feeds":
		return "chart_playlist.json"
	default:
		return "playlist.json"
	}
}

// write saves the phase's responses minified and trimmed, to stay far
// below the pre-commit hook's 1 MB limit, and indexes them, replacing
// older recordings.
func (r *recorder) write(t *testing.T) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	byFile := map[string]exchange{}
	if data, err := os.ReadFile(filepath.Join("testdata", indexFile)); err == nil {
		var old []exchange
		if err := json.Unmarshal(data, &old); err != nil {
			t.Fatal(err)
		}
		for _, ex := range old {
			byFile[ex.File] = ex
		}
	}
	for _, rec := range r.log {
		var v any
		if err := json.Unmarshal(rec.response, &v); err != nil {
			t.Fatalf("%s: %v", rec.File, err)
		}
		// Sorted keys, so that re-recording the same response changes nothing.
		data, err := json.Marshal(trim(v), json.Deterministic(true))
		if err != nil {
			t.Fatal(err)
		}
		if len(data) > 512<<10 {
			t.Errorf("%s is %d KB; trim more", rec.File, len(data)>>10)
		}
		if err := os.WriteFile(filepath.Join("testdata", rec.File), append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		byFile[rec.File] = rec.exchange
	}
	index := slices.Collect(maps.Values(byFile))
	slices.SortFunc(index, func(a, b exchange) int { return cmp.Compare(a.File, b.File) })
	data, err := json.Marshal(index, json.Deterministic(true), jsontext.WithIndent("  "))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("testdata", indexFile), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// dropped are keys no parser reads; tracking, accessibility, menus,
// buttons and descriptions make up most of a page. responseContext stays:
// home continuations need its visitorData.
var dropped = map[string]bool{
	"trackingParams": true, "clickTrackingParams": true, "loggingContext": true,
	"serviceTrackingParams": true, "responseId": true, "maxAgeSeconds": true, "maxAgeStoreSeconds": true,
	"frameworkUpdates": true, "microformat": true, "background": true, "accessibility": true,
	"accessibilityData": true, "accessibilityPlayData": true, "accessibilityPauseData": true,
	"menu": true, "buttons": true, "description": true, "multiSelectCheckbox": true, "playerParams": true,
	"straplineThumbnail": true, "shelfDivider": true, "displayPriority": true, "badges": true,
	"subtitleBadges": true, "contentsMultiSelectable": true, "itemHeight": true, "flexColumnDisplayStyle": true,
	"thumbnailCrop": true, "thumbnailScale": true, "playIcon": true, "pauseIcon": true, "playingIcon": true,
	"iconColor": true, "backgroundColor": true, "activeBackgroundColor": true, "loadingIndicatorColor": true,
	"iconLoadingColor": true, "activeScaleFactor": true, "buttonSize": true, "rippleTarget": true,
	"contentPosition": true, "displayStyle": true, "rendererContext": true,
}

func trim(v any) any {
	switch v := v.(type) {
	case map[string]any:
		for k, child := range v {
			if dropped[k] {
				delete(v, k)
				continue
			}
			v[k] = trim(child)
		}
	case []any:
		for i, child := range v {
			v[i] = trim(child)
		}
	}
	return v
}
