package loader

import (
	"errors"
	"testing"
	"time"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/catalog/catalogtest"
	"yt-music-player/internal/fetchstate"
)

// posts stands in for the UI thread: the test runs queued results itself.
type posts chan func()

// scripted returns a Loader over a catalogtest.Source the test answers,
// with an empty catalog.
func scripted(tb testing.TB) (catalogtest.Source, *Loader, *catalog.Catalog, posts) {
	src, cat, queue := catalogtest.New(tb), catalog.New(nil, nil, nil), make(posts, 16)
	l := New(src, &fetchstate.Registry{}, cat, func(fn func()) { queue <- fn })
	return src, l, cat, queue
}

// held waits for one result and returns it to run later.
func held(t *testing.T, queue posts) func() {
	t.Helper()
	select {
	case fn := <-queue:
		return fn
	case <-time.After(5 * time.Second):
		t.Fatal("no result came back")
		return nil
	}
}

func drain(queue posts) {
	for {
		select {
		case fn := <-queue:
			fn()
		default:
			return
		}
	}
}

func wantState(t *testing.T, l *Loader, res fetchstate.Resource, want fetchstate.State) {
	t.Helper()
	if got := l.states.State(res); got.State != want {
		t.Fatalf("%+v is %d (%v), want %d", res, got.State, got.Err, want)
	}
}

var (
	remote       = fetchstate.Resource{Kind: fetchstate.Album, ID: "remote"}
	remoteAlbum  = catalog.Album{ID: "remote", Title: "Far Signals", Artist: "Nova Static"}
	remoteTracks = []catalog.Song{
		{ID: "far-1", Title: "Carrier Wave", Artist: "Nova Static", AlbumID: "remote"},
		{ID: "far-2", Title: "Dead Air", Artist: "Nova Static", AlbumID: "remote"},
	}
)

func TestShowFetchesOnce(t *testing.T) {
	src, l, cat, queue := scripted(t)
	wantState(t, l, remote, fetchstate.Idle)
	l.Show(remote)
	call := src.Next(t, nil, "album", "remote")
	wantState(t, l, remote, fetchstate.Loading)
	if !l.IsBusy() {
		t.Error("not busy while fetching")
	}
	call.Reply <- catalogtest.Reply{Album: remoteAlbum, Songs: remoteTracks}
	held(t, queue)()
	wantState(t, l, remote, fetchstate.Loaded)
	if _, ok := cat.Album("remote"); !ok || len(cat.AlbumTracks("remote")) != 2 {
		t.Error("the album did not join the catalog")
	}
	if call.Ctx.Err() == nil || l.IsBusy() {
		t.Error("the finished fetch's context is still live")
	}
	// Fetched once per session: showing it again asks nothing.
	l.Show(fetchstate.Resource{})
	l.Show(remote)
	src.None(t, nil, 30*time.Millisecond)
}

func TestLeavingCancels(t *testing.T) {
	src, l, cat, queue := scripted(t)
	l.Show(remote)
	first := src.Next(t, nil, "album", "remote")
	// The result is already posted when the page is left; showing the page
	// again refetches.
	stale := remoteAlbum
	stale.Title = "Stale Signals"
	first.Reply <- catalogtest.Reply{Album: stale, Songs: remoteTracks}
	finish := held(t, queue)
	l.Show(HomeFeed)
	home := src.Next(t, nil, "home", "")
	if first.Ctx.Err() == nil {
		t.Fatal("leaving the page left its fetch running")
	}
	wantState(t, l, remote, fetchstate.Idle)
	l.Show(remote)
	second := src.Next(t, nil, "album", "remote")
	if home.Ctx.Err() == nil {
		t.Fatal("leaving Home left its fetch running")
	}
	finish()
	if _, ok := cat.Album("remote"); ok {
		t.Fatal("a canceled fetch's result was kept")
	}
	wantState(t, l, remote, fetchstate.Loading)
	// A late failure of the page left is dropped too.
	home.Reply <- catalogtest.Reply{Err: errors.New("late")}
	drain(queue)
	wantState(t, l, HomeFeed, fetchstate.Idle)
	second.Reply <- catalogtest.Reply{Album: remoteAlbum, Songs: remoteTracks}
	held(t, queue)()
	if album, _ := cat.Album("remote"); album.Title != "Far Signals" {
		t.Errorf("kept %q", album.Title)
	}
}

func TestFailedThenRetry(t *testing.T) {
	src, l, cat, queue := scripted(t)
	res := fetchstate.Resource{Kind: fetchstate.Playlist, ID: "remote-list"}
	l.Show(res)
	src.Next(t, nil, "playlist", res.ID).Reply <- catalogtest.Reply{Err: errors.New("network is down")}
	held(t, queue)()
	if fetch := l.states.State(res); fetch.State != fetchstate.Failed || fetch.Err == nil || fetch.Err.Error() != "network is down" {
		t.Fatalf("state %+v", fetch)
	}
	// A failure is not retried behind the user's back.
	src.None(t, nil, 30*time.Millisecond)

	l.Retry(res)
	wantState(t, l, res, fetchstate.Loading)
	playlist := catalog.Playlist{ID: res.ID, Name: "Night Shift", Songs: []string{"far-1"}}
	src.Next(t, nil, "playlist", res.ID).Reply <- catalogtest.Reply{Playlist: playlist, Songs: remoteTracks[:1]}
	held(t, queue)()
	if got, ok := cat.Playlist(res.ID); !ok || got.Name != "Night Shift" {
		t.Errorf("playlist not kept: %+v", got)
	}

	// Leaving a failed page and coming back tries again too.
	other := fetchstate.Resource{Kind: fetchstate.Playlist, ID: "other"}
	l.Show(other)
	src.Next(t, nil, "playlist", "other").Reply <- catalogtest.Reply{Err: errors.New("timeout")}
	held(t, queue)()
	l.Show(HomeFeed)
	src.Next(t, nil, "home", "").Reply <- catalogtest.Reply{}
	held(t, queue)()
	l.Show(other)
	src.Next(t, nil, "playlist", "other").Reply <- catalogtest.Reply{Err: catalog.ErrNotFound}
	held(t, queue)()
	wantState(t, l, other, fetchstate.Missing)
	// Missing is final: not asked again.
	l.Show(HomeFeed)
	l.Show(other)
	src.None(t, nil, 30*time.Millisecond)
}

func TestOnLoad(t *testing.T) {
	src, l, _, queue := scripted(t)
	res := fetchstate.Resource{Kind: fetchstate.Playlist, ID: "mix"}
	ran := 0
	l.OnLoad(res, func() { ran++ })
	l.Show(res)
	src.Next(t, nil, "playlist", "mix").Reply <- catalogtest.Reply{Playlist: catalog.Playlist{ID: "mix"}}
	held(t, queue)()
	if ran != 1 {
		t.Fatalf("ran %d times once loaded", ran)
	}

	// Leaving before it loads drops it, as does failing.
	other := fetchstate.Resource{Kind: fetchstate.Playlist, ID: "other"}
	l.OnLoad(other, func() { ran++ })
	l.Show(other)
	call := src.Next(t, nil, "playlist", "other")
	l.Show(HomeFeed)
	call.Reply <- catalogtest.Reply{}
	home := src.Next(t, nil, "home", "")
	l.Show(other)
	src.Next(t, nil, "playlist", "other").Reply <- catalogtest.Reply{Playlist: catalog.Playlist{ID: "other"}}
	home.Reply <- catalogtest.Reply{}
	held(t, queue)()
	l.OnLoad(res, func() { ran++ })
	l.Retry(res)
	src.Next(t, nil, "playlist", "mix").Reply <- catalogtest.Reply{Err: errors.New("offline")}
	held(t, queue)()
	l.Retry(res)
	src.Next(t, nil, "playlist", "mix").Reply <- catalogtest.Reply{Playlist: catalog.Playlist{ID: "mix"}}
	held(t, queue)()
	if ran != 1 {
		t.Errorf("ran %d times, want only the first", ran)
	}
}

func TestFeedsKeptUnique(t *testing.T) {
	src, l, cat, queue := scripted(t)
	l.Show(HomeFeed)
	albumItem := catalog.Item{Kind: catalog.ItemAlbum, Album: remoteAlbum}
	mix := catalog.Item{Kind: catalog.ItemPlaylist, Playlist: catalog.Playlist{ID: "mix", Name: "Mix"}}
	home := catalog.Home{Shelves: []catalog.Shelf{
		{Title: "Picks", Items: []catalog.Item{{Kind: catalog.ItemSong, Song: remoteTracks[0]}}},
		// Feeds repeat items.
		{Title: "New", Items: []catalog.Item{albumItem, albumItem, mix}},
	}}
	src.Next(t, nil, "home", "").Reply <- catalogtest.Reply{Home: home}
	held(t, queue)()
	if got := l.Home().Shelves; len(got) != 2 || len(got[1].Items) != 2 {
		t.Fatalf("home %+v", got)
	}
	_, song := cat.Song("far-1")
	_, album := cat.Album("remote")
	_, playlist := cat.Playlist("mix")
	if !song || !album || !playlist || len(cat.LibraryPlaylists()) != 0 {
		t.Error("the feed's music did not join the catalog, or joined the library")
	}

	l.Show(ExploreFeed)
	moods := []catalog.Mood{{Name: "Night drive", ID: "night"}, {Name: "Night drive", ID: "night"}, {Name: "Chill"}}
	src.Next(t, nil, "explore", "").Reply <- catalogtest.Reply{Explore: catalog.Explore{
		Moods: moods, Trending: []catalog.Song{remoteTracks[1], remoteTracks[1]},
	}}
	held(t, queue)()
	if explore := l.Explore(); len(explore.Moods) != 2 || len(explore.Trending) != 1 {
		t.Errorf("explore %+v", explore)
	}
	if _, ok := cat.Song("far-2"); !ok {
		t.Error("the chart did not join the catalog")
	}

	// A feed kept from elsewhere counts as fetched.
	l.KeepHome(catalog.Home{Shelves: []catalog.Shelf{{Items: []catalog.Item{mix, mix}}}})
	wantState(t, l, HomeFeed, fetchstate.Loaded)
	if count := len(l.Home().Shelves[0].Items); count != 1 {
		t.Errorf("kept %d items", count)
	}
}

func TestForget(t *testing.T) {
	src, l, _, queue := scripted(t)
	l.Show(HomeFeed)
	call := src.Next(t, nil, "home", "")
	// Shown: refetched at once.
	l.Forget(HomeFeed)
	if call.Ctx.Err() == nil {
		t.Fatal("forgetting the feed left its fetch running")
	}
	call.Reply <- catalogtest.Reply{}
	src.Next(t, nil, "home", "").Reply <- catalogtest.Reply{}
	held(t, queue)()
	wantState(t, l, HomeFeed, fetchstate.Loaded)
	// Not shown: refetched when next shown.
	l.Show(fetchstate.Resource{})
	l.Forget(HomeFeed)
	wantState(t, l, HomeFeed, fetchstate.Idle)
	src.None(t, nil, 50*time.Millisecond)
	l.Show(HomeFeed)
	src.Next(t, nil, "home", "").Reply <- catalogtest.Reply{}
	held(t, queue)()
	wantState(t, l, HomeFeed, fetchstate.Loaded)
}

func TestSearchDebouncesAndCancels(t *testing.T) {
	src, l, cat, queue := scripted(t)
	l.Debounce = 200 * time.Millisecond
	l.Search("o")
	l.Search("or")
	start := time.Now()
	l.Search("orb")
	l.Search("orb")
	// Typing a word asks once, for the word, after the debounce.
	call := src.Next(t, nil, "search", "orb")
	if elapsed := time.Since(start); elapsed < l.Debounce {
		t.Errorf("searched %v after typing, before the debounce", elapsed)
	}
	l.Debounce = 0
	call.Reply <- catalogtest.Reply{Results: catalog.Results{Songs: remoteTracks[:1]}}
	held(t, queue)()
	if found := l.Found(); found.Query != "orb" || len(found.Results.Songs) != 1 {
		t.Fatalf("found %+v", found)
	}
	if _, ok := cat.Song("far-1"); !ok {
		t.Error("a song found did not join the catalog")
	}
	// Searching for the shown query asks nothing.
	l.Search("orb")
	src.None(t, nil, 30*time.Millisecond)

	// A newer query cancels the search in flight even with its result
	// posted; the last results stay until the new ones arrive.
	l.Search("orbi")
	call = src.Next(t, nil, "search", "orbi")
	call.Reply <- catalogtest.Reply{Results: catalog.Results{Songs: remoteTracks}}
	finish := held(t, queue)
	l.Search("orbit")
	if call.Ctx.Err() == nil {
		t.Error("a newer query left the old search running")
	}
	finish()
	if found := l.Found(); found.Query != "orb" {
		t.Errorf("a canceled search's results were kept, for %q", found.Query)
	}
	src.Next(t, nil, "search", "orbit").Reply <- catalogtest.Reply{}
	held(t, queue)()
	if found := l.Found(); found.Query != "orbit" || !found.Results.IsEmpty() {
		t.Errorf("found %+v", found)
	}
}

func TestSearchErrorThenRetry(t *testing.T) {
	src, l, _, queue := scripted(t)
	l.Debounce = 0
	l.Search("zzz")
	src.Next(t, nil, "search", "zzz").Reply <- catalogtest.Reply{Err: errors.New("rate limited")}
	held(t, queue)()
	if found := l.Found(); found.FailedQuery != "zzz" || found.Err == nil || found.Query != "" {
		t.Fatalf("found %+v", found)
	}
	l.RetrySearch("zzz")
	if found := l.Found(); found.FailedQuery != "" {
		t.Error("still failed while retrying")
	}
	src.Next(t, nil, "search", "zzz").Reply <- catalogtest.Reply{}
	held(t, queue)()
	if found := l.Found(); found.Query != "zzz" {
		t.Fatalf("found %+v", found)
	}

	// Leaving search cancels the pending one and drops its answer.
	l.Debounce = time.Hour
	l.Search("zzzz")
	l.Search("")
	if l.IsBusy() {
		t.Error("search still pending after leaving search")
	}
	l.Debounce = 0
	l.Search("late")
	call := src.Next(t, nil, "search", "late")
	l.Stop()
	if call.Ctx.Err() == nil || l.IsBusy() {
		t.Error("Stop left the search running")
	}
	call.Reply <- catalogtest.Reply{Results: catalog.Results{Songs: remoteTracks}}
	drain(queue)
	if found := l.Found(); found.Query != "zzz" {
		t.Errorf("a canceled search's results were kept, for %q", found.Query)
	}
}

func TestRetryFailed(t *testing.T) {
	src, l, _, queue := scripted(t)
	l.Debounce = 0
	// Nothing failed: nothing is asked.
	l.RetryFailed()
	src.None(t, nil, 30*time.Millisecond)

	l.Show(remote)
	src.Next(t, nil, "album", "remote").Reply <- catalogtest.Reply{Err: errors.New("offline")}
	held(t, queue)()
	l.Search("zzz")
	src.Next(t, nil, "search", "zzz").Reply <- catalogtest.Reply{Err: errors.New("offline")}
	held(t, queue)()
	l.RetryFailed()
	wantState(t, l, remote, fetchstate.Loading)
	src.Take(t, nil, "album remote", "search zzz")
	drain(queue)

	// What failed but no longer shows waits to be shown again.
	l.Stop()
	l.Show(remote)
	src.Next(t, nil, "album", "remote").Reply <- catalogtest.Reply{Err: errors.New("offline")}
	held(t, queue)()
	l.Search("zzz")
	src.Next(t, nil, "search", "zzz").Reply <- catalogtest.Reply{Err: errors.New("offline")}
	held(t, queue)()
	l.Show(HomeFeed)
	src.Next(t, nil, "home", "").Reply <- catalogtest.Reply{}
	held(t, queue)()
	l.Search("")
	l.RetryFailed()
	src.None(t, nil, 30*time.Millisecond)
}
