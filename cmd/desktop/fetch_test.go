package main

import (
	"errors"
	"testing"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/catalog/catalogtest"
	"yt-music-player/internal/fetchstate"
	"yt-music-player/internal/loader"
	"yt-music-player/internal/session"

	"github.com/egoist/mygo/ui"
)

// reply answers a catalogtest.Source call.
type reply = catalogtest.Reply

// ytmdFailed is what the app shows of an error ytmd's source failed with:
// its text stays in the daemon, which may hold anything.
var ytmdFailed = api.ErrInternal.Error()

// anonApp is signed out, so only its pages ask the source for music.
func anonApp(tb testing.TB) (catalogtest.Source, *app, posts) {
	tb.Helper()
	src := catalogtest.New()
	a, q := startApp(tb, src, ytmdOptions{signedOut: true})
	q.ytmd.until(tb, "signed out", func() bool {
		return a.fetches.State(session.AccountRes).State == fetchstate.SignedOut
	})
	return src, a, q
}

// scriptedApp is an anonApp with empty feeds marked fetched, so only the
// pages a test opens ask for music.
func scriptedApp(tb testing.TB) (catalogtest.Source, *app, posts) {
	tb.Helper()
	src, a, q := anonApp(tb)
	fetchedFeeds(a)
	return src, a, q
}

// accountApp is over catalogtest.Accounts with empty feeds marked
// fetched, so ytmd asks first for the account, library and likes.
func accountApp(tb testing.TB) (catalogtest.Accounts, *app, posts) {
	tb.Helper()
	src := catalogtest.Accounts{Source: catalogtest.New()}
	a, q := sourceApp(tb, src)
	fetchedFeeds(a)
	return src, a, q
}

func fetchedFeeds(a *app) {
	a.fetches.Set(loader.HomeFeed, fetchstate.Fetch{State: fetchstate.Loaded})
	a.fetches.Set(loader.ExploreFeed, fetchstate.Fetch{State: fetchstate.Loaded})
}

// runPosts runs what was posted once ytmd answered all it was asked.
func runPosts(t testing.TB, tt *ui.Tester, q posts) {
	t.Helper()
	syncDaemon(t, q)
	drain(q)
	tt.Frame()
}

// heldPost waits for one of the app's results, running the Client's
// posts meanwhile, and returns it unrun, so a test can run it after making
// it stale.
func heldPost(t testing.TB, q posts) func() {
	t.Helper()
	timeout := time.After(wait)
	for {
		select {
		case fn := <-q.app:
			return fn
		case fn := <-q.client:
			fn()
		case <-timeout:
			t.Fatal("no result came back")
			return nil
		}
	}
}

// answer replies to call and waits until the app has the answer: ytmd
// took it, the loader's result for a page came back, and what the daemon
// sent with it arrived.
func answer(t *testing.T, tt *ui.Tester, q posts, call catalogtest.Call, r reply) {
	t.Helper()
	src := q.ytmd.src
	n := src.count(call.Kind, call.ID)
	call.Reply <- r
	q.ytmd.until(t, "answered "+call.Kind, func() bool { return src.count(call.Kind, call.ID) > n })
	if isPage(call) {
		heldPost(t, q)()
	}
	syncDaemon(t, q)
	tt.Frame()
}

// isPage reports whether the loader asked for call, rather than ytmd's
// account.
func isPage(call catalogtest.Call) bool {
	switch call.Kind {
	case "album", "search", "home", "explore":
		return true
	case "playlist":
		return call.ID != catalog.LikedID
	}
	return false
}

func waitPost(t *testing.T, tt *ui.Tester, q posts) {
	t.Helper()
	heldPost(t, q)()
	tt.Frame()
}

// canceled waits until ytmd canceled call: the app's cancel crosses the
// socket.
func canceled(call catalogtest.Call) bool {
	select {
	case <-call.Ctx.Done():
		return true
	case <-time.After(wait):
		return false
	}
}

// remoteAlbum and remoteTracks are not in the placeholder catalog.
var (
	remoteAlbum = catalog.Album{
		ID: "remote", Title: "Far Signals", Artist: "Nova Static", Year: 2026, Kind: catalog.KindEP, Art: "neon-tide",
	}
	remoteTracks = []catalog.Song{
		{
			ID: "far-1", Title: "Carrier Wave", Artist: "Nova Static",
			Album: "Far Signals", AlbumID: "remote", Seconds: 201, Art: "neon-tide",
		},
		{
			ID: "far-2", Title: "Dead Air", Artist: "Nova Static",
			Album: "Far Signals", AlbumID: "remote", Seconds: 187, Art: "neon-tide",
		},
	}
)

func TestAlbumLoadsThenShows(t *testing.T) {
	src, a, q := scriptedApp(t)
	tt := newTester(a)
	a.router.Push(albumPath("remote"))
	c := src.Next(t, tt.Frame, "album", "remote")
	if !tt.HasText("Loading…") || tt.HasText("Far Signals") || a.router.Title() != "Album" {
		t.Fatalf("loading page wrong, title %q, texts %q", a.router.Title(), tt.Texts())
	}

	answer(t, tt, q, c, reply{Album: remoteAlbum, Songs: remoteTracks})
	if !tt.HasText("Far Signals") || !tt.HasText("Dead Air") || tt.HasText("Loading…") {
		t.Fatalf("album not shown, texts %q", tt.Texts())
	}
	if a.router.Title() != "Far Signals" {
		t.Errorf("title %q", a.router.Title())
	}
	if c.Ctx.Err() == nil {
		t.Error("the finished fetch's context is still live")
	}
	// The fetched songs joined the catalog, so they can play.
	click(t, tt, "Dead Air by Nova Static")
	wantPlaying(t, a, "far-2")

	// Fetched once per session: coming back does not ask again.
	click(t, tt, "Back")
	click(t, tt, "Forward")
	src.None(t, tt.Frame, 50*time.Millisecond)
	if !tt.HasText("Far Signals") {
		t.Errorf("album gone on returning, texts %q", tt.Texts())
	}
}

func TestCachedAlbumShowsWhileLoading(t *testing.T) {
	src, a, q := scriptedApp(t)
	tt := newTester(a)
	a.router.Push(albumPath("velvet-static"))
	c := src.Next(t, tt.Frame, "album", "velvet-static")
	if !tt.HasText("Static Bloom") || tt.HasText("Loading…") || a.router.Title() != "Velvet Static" {
		t.Fatalf("the catalog's album not shown while loading, texts %q", tt.Texts())
	}
	al, _ := a.cat.Album("velvet-static")
	al.Title = "Velvet Static (Deluxe)"
	answer(t, tt, q, c, reply{Album: al, Songs: a.cat.AlbumTracks(al.ID)})
	if !tt.HasText("Velvet Static (Deluxe)") {
		t.Errorf("fetched album not shown, texts %q", tt.Texts())
	}
}

func TestPlaylistErrorThenRetry(t *testing.T) {
	src, a, q := scriptedApp(t)
	tt := newTester(a)
	a.router.Push(playlistPath("remote-list"))
	answer(t, tt, q, src.Next(t, tt.Frame, "playlist", "remote-list"), reply{Err: errors.New("network is down")})
	if !tt.HasText("Couldn’t load this playlist") || !tt.HasText(ytmdFailed) || a.router.Title() != "Playlist" {
		t.Fatalf("no error, title %q, texts %q", a.router.Title(), tt.Texts())
	}
	// A failure is not retried behind the user's back.
	src.None(t, tt.Frame, 50*time.Millisecond)

	click(t, tt, "Retry")
	c := src.Next(t, tt.Frame, "playlist", "remote-list")
	if !tt.HasText("Loading…") {
		t.Errorf("retry not loading, texts %q", tt.Texts())
	}
	pl := catalog.Playlist{ID: "remote-list", Name: "Night Shift", Owner: "Lan", Songs: []string{"far-1"}}
	answer(t, tt, q, c, reply{Playlist: pl, Songs: remoteTracks[:1]})
	if !tt.HasText("Night Shift") || !tt.HasText("Carrier Wave") {
		t.Fatalf("playlist not shown after retry, texts %q", tt.Texts())
	}

	// Leaving a failed page and coming back also retries.
	a.router.Push(playlistPath("other"))
	answer(t, tt, q, src.Next(t, tt.Frame, "playlist", "other"), reply{Err: errors.New("timeout")})
	a.router.Push(pathHome)
	tt.Frame()
	a.router.Push(playlistPath("other"))
	answer(t, tt, q, src.Next(t, tt.Frame, "playlist", "other"), reply{Err: catalog.ErrNotFound})
	if !tt.HasText("Nothing here") {
		t.Errorf("no not-found page, texts %q", tt.Texts())
	}
}

func TestNotFound(t *testing.T) {
	for _, path := range []string{albumPath("nope"), playlistPath("nope")} {
		a, q := testApp(t)
		tt := newTester(a)
		a.router.Push(path)
		tt.Frame()
		// Unknown is not missing until the source says so.
		if tt.HasText("Nothing here") || !tt.HasText("Loading…") {
			t.Errorf("%s: not found before the source answered, texts %q", path, tt.Texts())
		}
		settleLoads(t, tt, a, q)
		if !tt.HasText("Nothing here") {
			t.Errorf("%s: no not-found page, texts %q", path, tt.Texts())
		}
	}
}

func TestStaleFetchRejected(t *testing.T) {
	src, a, q := scriptedApp(t)
	tt := newTester(a)
	a.router.Push(albumPath("remote"))
	first := src.Next(t, tt.Frame, "album", "remote")
	// The result is posted but not yet run when the page is left, and
	// returning to the page fetches anew.
	stale := remoteAlbum
	stale.Title = "Stale Signals"
	first.Reply <- reply{Album: stale, Songs: remoteTracks}
	finish := heldPost(t, q)
	a.router.Push(pathHome)
	tt.Frame()
	if first.Ctx.Err() == nil {
		t.Fatal("leaving the page left its fetch running")
	}
	a.router.Push(albumPath("remote"))
	second := src.Next(t, tt.Frame, "album", "remote")
	finish()
	tt.Frame()
	if _, ok := a.cat.Album("remote"); ok || !tt.HasText("Loading…") {
		t.Fatalf("a canceled fetch's result was used, texts %q", tt.Texts())
	}
	answer(t, tt, q, second, reply{Album: remoteAlbum, Songs: remoteTracks})
	if !tt.HasText("Far Signals") {
		t.Fatalf("album not shown, texts %q", tt.Texts())
	}

	// A late failure of a left page is rejected too.
	a.router.Push(albumPath("gone"))
	src.Next(t, tt.Frame, "album", "gone").Reply <- reply{Err: catalog.ErrNotFound}
	finish = heldPost(t, q)
	a.router.Back()
	tt.Frame()
	finish()
	tt.Frame()
	gone := a.fetches.State(fetchstate.Resource{Kind: fetchstate.Album, ID: "gone"})
	if gone.State != fetchstate.Idle || !tt.HasText("Far Signals") {
		t.Errorf("a canceled fetch's error was recorded, texts %q", tt.Texts())
	}
	a.router.Forward()
	answer(t, tt, q,
		src.Next(t, tt.Frame, "album", "gone"),
		reply{Album: catalog.Album{ID: "gone", Title: "Back Again"}},
	)
	if !tt.HasText("Back Again") {
		t.Errorf("album not shown, texts %q", tt.Texts())
	}
}

func TestSearchDebouncesAndCancels(t *testing.T) {
	src, a, q := scriptedApp(t)
	tt := newTester(a)
	// Long enough to type a word in, even with the race detector slowing
	// each letter's frame.
	a.loader.Debounce = time.Second
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Type("o")
	tt.Type("r")
	last := time.Now()
	tt.Type("b")
	// Typing a word searches once, for the whole word, after the debounce.
	c := src.Next(t, tt.Frame, "search", "orb")
	if d := time.Since(last); d < a.loader.Debounce {
		t.Errorf("searched %v after typing, before the debounce", d)
	}
	a.loader.Debounce = loader.SearchDebounce
	if tt.HasText("No results for “orb”") || !tt.HasText("Searching for “orb”…") {
		t.Errorf("searching page wrong, texts %q", tt.Texts())
	}
	found, err := catalog.NewPlaceholderSource(0).Search(t.Context(), "orb")
	if err != nil {
		t.Fatal(err)
	}
	answer(t, tt, q, c, reply{Results: found})
	if !tt.HasText("Top result") || !tt.HasText("“orb”") {
		t.Fatalf("no results for orb, texts %q", tt.Texts())
	}

	// A newer query cancels the search in flight, even with its results
	// posted, and the last results stay until the new ones come.
	tt.Type("i")
	c = src.Next(t, tt.Frame, "search", "orbi")
	c.Reply <- reply{Results: catalog.Results{Songs: remoteTracks}}
	finish := heldPost(t, q)
	tt.Type("t")
	tt.Frame()
	if c.Ctx.Err() == nil {
		t.Error("typing on left the old search running")
	}
	finish()
	tt.Frame()
	if tt.HasText("Carrier Wave") {
		t.Error("a canceled search's results were shown")
	}
	if !tt.HasText("Top result") || !tt.HasText("SEARCHING…") || !tt.HasText("“orb”") {
		t.Errorf("previous results gone while searching, texts %q", tt.Texts())
	}
	c = src.Next(t, tt.Frame, "search", "orbit")
	src.None(t, tt.Frame, loader.SearchDebounce+50*time.Millisecond)
	answer(t, tt, q, c, reply{Results: catalog.Results{Songs: remoteTracks, Albums: []catalog.Album{remoteAlbum}}})
	if !tt.HasText("Carrier Wave") || !tt.HasText("“orbit”") || tt.HasText("SEARCHING…") {
		t.Fatalf("results for orbit not shown, texts %q", tt.Texts())
	}
	// Found music joined the catalog, so its album shows from the cache.
	click(t, tt, "Far Signals")
	wantPath(t, a, albumPath("remote"))
	c = src.Next(t, tt.Frame, "album", "remote")
	if !tt.HasText("Far Signals") || !tt.HasText("Dead Air") || tt.HasText("Loading…") {
		t.Errorf("found album not shown from the catalog, texts %q", tt.Texts())
	}
	answer(t, tt, q, c, reply{Album: remoteAlbum, Songs: remoteTracks})
}

func TestSearchErrorThenRetry(t *testing.T) {
	src, a, q := scriptedApp(t)
	tt := newTester(a)
	a.router.Push(searchPath("zzz"))
	answer(t, tt, q, src.Next(t, tt.Frame, "search", "zzz"), reply{Err: errors.New("rate limited")})
	showsErr := tt.HasText("Couldn’t search for “zzz”") && tt.HasText(ytmdFailed)
	if !showsErr || tt.HasText("No results for “zzz”") {
		t.Fatalf("no search error, texts %q", tt.Texts())
	}
	click(t, tt, "Retry")
	c := src.Next(t, tt.Frame, "search", "zzz")
	if tt.HasText("No results for “zzz”") {
		t.Error("no results shown before the search completed")
	}
	answer(t, tt, q, c, reply{})
	if !tt.HasText("No results for “zzz”") {
		t.Errorf("no empty results, texts %q", tt.Texts())
	}
	// Leaving search cancels a pending one.
	a.router.Push(searchPath("zzzz"))
	tt.Frame()
	a.router.Push(pathHome)
	tt.Frame()
	if busy(a) {
		t.Error("search still pending after leaving search")
	}
	// A slow frame may let the debounce pass first; the search is
	// canceled all the same, and its answer is dropped.
	select {
	case late := <-src.Calls:
		if !canceled(late) {
			t.Errorf("search for %q still running after leaving search", late.ID)
		}
		late.Reply <- reply{Results: catalog.Results{Songs: remoteTracks}}
	case <-time.After(a.loader.Debounce + 50*time.Millisecond):
	}
	runPosts(t, tt, q)
	if query := a.loader.Found().Query; query != "zzz" {
		t.Errorf("a canceled search's results were kept, for %q", query)
	}
}
