package main

import (
	"errors"
	"slices"
	"testing"
	"time"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/catalog/catalogtest"
	"yt-music-player/internal/fetchstate"
	"yt-music-player/internal/loader"

	"github.com/egoist/mygo/ui"
)

// remoteMix and remoteList are playlists a feed offers without their songs.
var (
	remoteMix = catalog.Playlist{
		ID: "remote-mix", Name: "Night Shift Mix", Owner: "Nova Static and more", Art: "small-hours",
	}
	remoteList = catalog.Playlist{ID: "remote-list", Name: "Static Hours", Owner: "Lan", Art: "low-orbit"}
)

// remoteHome is a feed of music the UI's catalog does not have yet.
func remoteHome() catalog.Home {
	songs := catalog.Shelf{Title: "Fresh picks", Strapline: "Start radio from a song"}
	for _, s := range remoteTracks {
		songs.Items = append(songs.Items, catalog.Item{Kind: catalog.ItemSong, Song: s})
	}
	return catalog.Home{Shelves: []catalog.Shelf{
		songs,
		{Title: "New for you", Items: []catalog.Item{
			{Kind: catalog.ItemAlbum, Album: remoteAlbum},
			// Feeds repeat themselves.
			{Kind: catalog.ItemAlbum, Album: remoteAlbum},
			{Kind: catalog.ItemPlaylist, Playlist: remoteMix},
			{Kind: catalog.ItemPlaylist, Playlist: remoteList},
		}},
		{Title: "Artists to follow", Items: []catalog.Item{
			{Kind: catalog.ItemArtist, Artist: catalog.Artist{Name: "Nova Static", Art: "neon-tide"}},
		}},
	}}
}

// feedApp is an anonApp on Home whose home feed was answered with home.
func feedApp(t *testing.T, home catalog.Home) (catalogtest.Source, *app, posts, *ui.Tester) {
	t.Helper()
	src, a, q := anonApp(t)
	tt := newTester(a)
	answer(t, tt, q, src.Next(t, tt.Frame, "home", ""), reply{Home: home})
	return src, a, q, tt
}

func TestHomeLoadsThenShows(t *testing.T) {
	src, a, q := anonApp(t)
	tt := newTester(a)
	call := src.Next(t, tt.Frame, "home", "")
	if !tt.HasText("Loading…") || tt.HasText("Fresh picks") || a.router.Title() != "Home" {
		t.Fatalf("loading home wrong, title %q, texts %q", a.router.Title(), tt.Texts())
	}

	answer(t, tt, q, call, reply{Home: remoteHome()})
	wantTexts(t, tt, "Fresh picks", "Play all", "New for you", "Far Signals", "Artists to follow", "Nova Static and more")
	if tt.HasText("Loading…") || tt.HasText("Quick picks") {
		t.Errorf("home still shows the placeholder or loading, texts %q", tt.Texts())
	}
	// The hero features the feed's first playlist.
	if !tt.HasText("Night Shift Mix") || !tt.HasText("Open") {
		t.Errorf("no hero, texts %q", tt.Texts())
	}
	if call.Ctx.Err() == nil {
		t.Error("the finished fetch's context is still live")
	}
	if items := a.loader.Home().Shelves[1].Items; len(items) != 3 {
		t.Errorf("a repeated item was kept: %+v", items)
	}

	// The feed's music joins the catalog, but its playlists stay out of the
	// library.
	click(t, tt, "Dead Air by Nova Static")
	wantPlaying(t, a, "far-2")
	if _, ok := a.cat.Playlist(remoteMix.ID); !ok || slices.Contains(libraryIDs(a.cat), remoteMix.ID) {
		t.Error("the feed's playlist is not cached, or joined the library")
	}

	// Feeds load once a session: Home comes back without asking again.
	click(t, tt, "Explore")
	answer(t, tt, q, src.Next(t, tt.Frame, "explore", ""), reply{})
	click(t, tt, "Home")
	src.None(t, tt.Frame, 50*time.Millisecond)
	if !tt.HasText("Fresh picks") {
		t.Errorf("home gone on returning, texts %q", tt.Texts())
	}
}

func TestHomeShelvesOpenAndPlay(t *testing.T) {
	src, a, q, tt := feedApp(t, remoteHome())

	click(t, tt, "Play all")
	st := state(t, a)
	var upNext []string
	for _, s := range a.upNext() {
		upNext = append(upNext, s.ID)
	}
	if st.Current != "far-1" || !st.Playing || !slices.Equal(upNext, []string{"far-2"}) {
		t.Errorf("Play all at %q (%v), up next %q", st.Current, st.Playing, upNext)
	}

	// The album shows the feed's songs while it loads.
	click(t, tt, "Far Signals")
	wantPath(t, a, albumPath(remoteAlbum.ID))
	call := src.Next(t, tt.Frame, "album", remoteAlbum.ID)
	if !tt.HasText("Dead Air") || tt.HasText("Loading…") {
		t.Errorf("feed album page wrong while loading, texts %q", tt.Texts())
	}
	answer(t, tt, q, call, reply{Album: remoteAlbum, Songs: remoteTracks})
	if !tt.HasText("Carrier Wave") {
		t.Errorf("album not shown, texts %q", tt.Texts())
	}

	click(t, tt, "Back")
	// A feed's playlist has no songs until fetched; it must not look empty
	// meanwhile.
	click(t, tt, "Static Hours")
	wantPath(t, a, playlistPath(remoteList.ID))
	call = src.Next(t, tt.Frame, "playlist", remoteList.ID)
	if !tt.HasText("Loading…") || tt.HasText("Nothing here yet") {
		t.Errorf("feed playlist page wrong while loading, texts %q", tt.Texts())
	}
	answer(t, tt, q, call, reply{Err: errors.New("offline")})

	// The hero opens its playlist too.
	click(t, tt, "Back")
	click(t, tt, "Open")
	wantPath(t, a, playlistPath(remoteMix.ID))
	answer(t, tt, q, src.Next(t, tt.Frame, "playlist", remoteMix.ID), reply{Playlist: remoteMix})

	// Artists open nothing yet.
	click(t, tt, "Back")
	click(t, tt, "Nova Static")
	wantPath(t, a, pathHome)
}

func TestHeroPlaysItsPlaylist(t *testing.T) {
	src, a, q, tt := feedApp(t, remoteHome())
	// Its songs are unknown: Play opens it and plays once they come.
	click(t, tt, "Play")
	wantPath(t, a, playlistPath(remoteMix.ID))
	call := src.Next(t, tt.Frame, "playlist", remoteMix.ID)
	if state(t, a).Current == "far-2" {
		t.Fatal("played before the songs came")
	}
	mix := remoteMix
	mix.Songs = []string{"far-2", "far-1"}
	answer(t, tt, q, call, reply{Playlist: mix, Songs: []catalog.Song{remoteTracks[1], remoteTracks[0]}})
	wantPlaying(t, a, "far-2")

	// Now known, it plays at once from Home.
	farOne, _ := a.cat.Song("far-1")
	a.playSong(&farOne)
	click(t, tt, "Back")
	click(t, tt, "Play")
	wantPath(t, a, pathHome)
	wantPlaying(t, a, "far-2")

	// Leaving its page before the songs come cancels the play.
	src, a, q, tt = feedApp(t, remoteHome())
	before := a.currentID()
	click(t, tt, "Play")
	call = src.Next(t, tt.Frame, "playlist", remoteMix.ID)
	click(t, tt, "Back")
	call.Reply <- reply{Playlist: mix, Songs: remoteTracks}
	runPosts(t, tt, q)
	a.router.Forward()
	answer(t, tt, q, src.Next(t, tt.Frame, "playlist", remoteMix.ID), reply{Playlist: mix, Songs: remoteTracks})
	if st := state(t, a); st.Current != before || st.Playing {
		t.Errorf("playing %q (%v) after leaving the hero's playlist", st.Current, st.Playing)
	}
}

func TestHomeWithoutPlaylistsHasNoHero(t *testing.T) {
	h := remoteHome()
	h.Shelves = h.Shelves[:1]
	_, _, _, tt := feedApp(t, h)
	if !tt.HasText("Fresh picks") || tt.HasText("Open") {
		t.Errorf("home wrong without playlists, texts %q", tt.Texts())
	}
}

func TestHomeErrorThenRetry(t *testing.T) {
	src, a, q := anonApp(t)
	tt := newTester(a)
	answer(t, tt, q, src.Next(t, tt.Frame, "home", ""), reply{Err: errors.New("network is down")})
	if !tt.HasText("Couldn’t load your home") || !tt.HasText(ytmdFailed) {
		t.Fatalf("no error, texts %q", tt.Texts())
	}
	// A failure is not retried behind the user's back.
	src.None(t, tt.Frame, 50*time.Millisecond)

	click(t, tt, "Retry")
	call := src.Next(t, tt.Frame, "home", "")
	if !tt.HasText("Loading…") {
		t.Errorf("retry not loading, texts %q", tt.Texts())
	}
	answer(t, tt, q, call, reply{Home: catalog.Home{Shelves: []catalog.Shelf{{Title: "Empty shelf"}}}})
	if !tt.HasText("Nothing to recommend yet") || tt.HasText("Empty shelf") {
		t.Errorf("no empty state, texts %q", tt.Texts())
	}
}

func TestStaleFeedRejected(t *testing.T) {
	src, a, q := anonApp(t)
	tt := newTester(a)
	first := src.Next(t, tt.Frame, "home", "")
	stale := remoteHome()
	stale.Shelves[0].Title = "Stale picks"
	first.Reply <- reply{Home: stale}
	finish := heldPost(t, q)
	// Leave Home while its result is being posted.
	a.router.Push(pathExplore)
	explore := src.Next(t, tt.Frame, "explore", "")
	if !canceled(first) {
		t.Fatal("leaving Home left its fetch running")
	}
	a.router.Back()
	second := src.Next(t, tt.Frame, "home", "")
	if !canceled(explore) {
		t.Fatal("leaving Explore left its fetch running")
	}
	finish()
	tt.Frame()
	if tt.HasText("Stale picks") || !tt.HasText("Loading…") || len(a.loader.Home().Shelves) != 0 {
		t.Fatalf("a canceled fetch's feed was used, texts %q", tt.Texts())
	}
	// A late failure of the page left is dropped too.
	explore.Reply <- reply{Err: errors.New("late")}
	runPosts(t, tt, q)
	if a.fetches.Get(loader.ExploreRes).State != fetchstate.Idle {
		t.Error("a canceled fetch's error was recorded")
	}
	answer(t, tt, q, second, reply{Home: remoteHome()})
	if !tt.HasText("Fresh picks") {
		t.Errorf("home not shown, texts %q", tt.Texts())
	}
}

func TestExploreLoadsThenShows(t *testing.T) {
	src, a, q := scriptedApp(t)
	a.loader.Forget(loader.ExploreRes)
	tt := newTester(a)
	a.router.Push(pathExplore)
	call := src.Next(t, tt.Frame, "explore", "")
	if !tt.HasText("Loading…") || tt.HasText("Trending") || a.router.Title() != "Explore" {
		t.Fatalf("loading explore wrong, texts %q", tt.Texts())
	}
	answer(t, tt, q, call, reply{Err: errors.New("rate limited")})
	if !tt.HasText("Couldn’t load Explore") || !tt.HasText(ytmdFailed) {
		t.Fatalf("no error, texts %q", tt.Texts())
	}

	click(t, tt, "Retry")
	call = src.Next(t, tt.Frame, "explore", "")
	moods := []catalog.Mood{
		{Name: "Night drive", ID: "night", Tint: catalog.Tint{L: 0.7, C: 0.1, H: 250}},
		{Name: "Night drive", ID: "night"},
	}
	answer(t, tt, q, call, reply{Explore: catalog.Explore{
		NewReleases: []catalog.Album{remoteAlbum},
		Moods:       moods,
		Trending:    remoteTracks,
	}})
	wantTexts(t, tt, "Night drive", "New albums & singles", "Far Signals", "Trending", "Carrier Wave")
	if moods := a.loader.Explore().Moods; len(moods) != 1 {
		t.Errorf("a repeated mood was kept: %+v", moods)
	}
	// Moods open nothing yet; the chart plays and releases open.
	click(t, tt, "Night drive")
	wantPath(t, a, pathExplore)
	// The chart is at the bottom, out of view.
	tt.Scroll(600, 400, 0, 2000)
	frames(tt, 2)
	click(t, tt, "Dead Air by Nova Static")
	wantPlaying(t, a, "far-2")
	tt.Scroll(600, 400, 0, -2000)
	frames(tt, 2)
	click(t, tt, "Far Signals")
	wantPath(t, a, albumPath(remoteAlbum.ID))
	answer(t, tt, q, src.Next(t, tt.Frame, "album", remoteAlbum.ID), reply{Album: remoteAlbum, Songs: remoteTracks})
}

func TestExploreEmpty(t *testing.T) {
	src, a, q := scriptedApp(t)
	a.loader.Forget(loader.ExploreRes)
	tt := newTester(a)
	a.router.Push(pathExplore)
	answer(t, tt, q, src.Next(t, tt.Frame, "explore", ""), reply{})
	if !tt.HasText("Nothing to explore right now") {
		t.Errorf("no empty state, texts %q", tt.Texts())
	}
}

func TestLibraryKeepsFeedPlaylistsOut(t *testing.T) {
	a, q := testApp(t)
	tt := newTester(a)
	settleLoads(t, tt, a, q)
	if !tt.HasText("My Supermix") {
		t.Fatalf("home not loaded, texts %q", tt.Texts())
	}
	a.router.Push(libraryPath(tabPlaylists))
	frames(tt, 3)
	if tt.HasText("My Supermix") || !tt.HasText("Late Night Drive") {
		t.Errorf("library lists the feed's mixes, texts %q", tt.Texts())
	}
}
