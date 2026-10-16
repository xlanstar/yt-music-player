package main

import (
	"context"
	"math"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/theme"

	"github.com/egoist/mygo/ui"
)

// testApp's placeholder source answers at once; the account's fetches
// have come.
func testApp(tb testing.TB) (*app, posts) {
	tb.Helper()
	a, q := sourceApp(tb, catalog.NewPlaceholderSource(0))
	q.ytmd.until(tb, "fetched the account", func() bool { return !a.session.IsBusy() })
	return a, q
}

// sourceApp is an app over ytmd serving src; it starts with the
// placeholder's music whatever src is.
func sourceApp(tb testing.TB, src catalog.Source) (*app, posts) {
	tb.Helper()
	return startApp(tb, src, ytmdOptions{})
}

// idleApp is for tests that wait for no fetch: feeds, account, library and
// likes are preloaded, and other pages' fetches never answer.
func idleApp(tb testing.TB) *app {
	tb.Helper()
	a, _ := testApp(tb)
	return preload(a)
}

func preload(a *app) *app {
	ctx := context.Background()
	src := catalog.NewPlaceholderSource(0)
	h, err := src.Home(ctx)
	must(err)
	a.loader.KeepHome(h)
	e, err := src.Explore(ctx)
	must(err)
	a.loader.KeepExplore(e)
	account, err := src.Account(ctx)
	must(err)
	lib, err := src.Library(ctx)
	must(err)
	a.session.KeepAccount(account, lib)
	liked, songs, err := src.Playlist(ctx, catalog.LikedID)
	must(err)
	a.cat.Add(songs, nil, nil)
	a.session.KeepLikes(liked.Songs)
	return a
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func busy(a *app) bool {
	return a.loader.IsBusy() || a.art != nil && a.art.Loading() || a.session.IsBusy()
}

// settleLoads draws frames and runs posts until nothing loads, no post is
// due and a round trip to ytmd brought nothing more.
func settleLoads(t testing.TB, tt *ui.Tester, a *app, q posts) {
	t.Helper()
	timeout := time.After(wait)
	for {
		tt.Frame()
		if !busy(a) && len(q.app) == 0 && len(q.client) == 0 {
			syncDaemon(t, q)
			tt.Frame()
			if !busy(a) && len(q.app) == 0 && len(q.client) == 0 {
				return
			}
		}
		select {
		case fn := <-q.app:
			fn()
		case fn := <-q.client:
			fn()
		case <-timeout:
			t.Fatalf("still loading, texts %q", tt.Texts())
		}
	}
}

// settled waits for what the daemon does with the controls sent so far.
func settled(t testing.TB, a *app) {
	t.Helper()
	syncDaemon(t, ytmdOf(a).q)
}

func click(t *testing.T, tt *ui.Tester, s string) {
	t.Helper()
	if err := tt.Click(s); err != nil {
		t.Fatal(err)
	}
}

// wantPlaying waits for the controls sent, then wants id playing.
func wantPlaying(t *testing.T, a *app, id string) {
	t.Helper()
	settled(t, a)
	if st := a.mirror.State(); st.Current != id || !st.Playing {
		t.Fatalf("playing %q (%v), want %q", st.Current, st.Playing, id)
	}
}

// state is the daemon's, once it did what the app asked.
func state(t testing.TB, a *app) api.State {
	t.Helper()
	settled(t, a)
	return a.mirror.State()
}

func wantPath(t *testing.T, a *app, loc string) {
	t.Helper()
	if got := a.router.Location(); got != loc {
		t.Fatalf("at %q, want %q", got, loc)
	}
}

func wantTexts(t *testing.T, tt *ui.Tester, texts ...string) {
	t.Helper()
	for _, s := range texts {
		if !tt.HasText(s) {
			t.Errorf("no %q, texts %q", s, tt.Texts())
		}
	}
}

func count(texts []string, s string) int {
	n := 0
	for _, text := range texts {
		if text == s {
			n++
		}
	}
	return n
}

func TestPlayPause(t *testing.T) {
	a := idleApp(t)
	tt := newTester(a)
	click(t, tt, "Play")
	if !state(t, a).Playing {
		t.Fatal("not playing after clicking Play")
	}
	tt.Frame()
	tt.Frame()
	tt.Key(0, ui.KeySpace)
	if state(t, a).Playing {
		t.Error("still playing after Space")
	}
}

func TestSongRowPlaysAndLikes(t *testing.T) {
	a := idleApp(t)
	tt := newTester(a)
	moreButtons := count(tt.Texts(), "More")
	click(t, tt, "Paper Moons by Ivy Calder")
	wantPlaying(t, a, "paper-moons")
	// Focus within the row keeps its actions shown, for keyboard users.
	tt.Move(1, 1)
	if n := count(tt.Texts(), "More"); n != moreButtons+1 {
		t.Errorf("%d More buttons with a row focused, want %d", n, moreButtons+1)
	}
	// A liked row keeps its Like button, beside the song's, not inside it.
	click(t, tt, "Late Night Drive")
	click(t, tt, "Like")
	if a.session.Liked("midnight-arcade") {
		t.Error("Unlike left midnight-arcade liked")
	}
}

func TestNavigationAndHistory(t *testing.T) {
	a, q := testApp(t)
	tt := newTester(a)
	// The sidebar lists the library once it loads.
	settleLoads(t, tt, a, q)
	click(t, tt, "Explore")
	if !tt.HasText("Moods & genres") {
		t.Fatalf("explore not shown, texts %q", tt.Texts())
	}
	click(t, tt, "Library")
	click(t, tt, "Back")
	wantPath(t, a, pathExplore)
	click(t, tt, "Forward")
	wantPath(t, a, pathLibrary)
	click(t, tt, "Songs")
	wantPath(t, a, libraryPath(tabSongs))
	click(t, tt, "Late Night Drive")
	wantPath(t, a, playlistPath("late-night-drive"))
	settleLoads(t, tt, a, q)
	if !tt.HasText("TITLE") || !tt.HasText("ALBUM") {
		t.Errorf("playlist not shown, texts %q", tt.Texts())
	}
	click(t, tt, "Back")
	wantPath(t, a, libraryPath(tabSongs))
}

func TestHistoryKeys(t *testing.T) {
	a := idleApp(t)
	tt := newTester(a)
	click(t, tt, "Explore")
	tt.Key(0, ui.KeyBack)
	wantPath(t, a, pathHome)
	tt.Key(0, ui.KeyForward)
	wantPath(t, a, pathExplore)
	mods, back := ui.Alt, ui.KeyLeft
	if runtime.GOOS == "darwin" {
		mods, back = ui.Super, ui.KeyBracketLeft
	}
	tt.Key(mods, back)
	wantPath(t, a, pathHome)
}

func TestAlbumPage(t *testing.T) {
	a, q := testApp(t)
	tt := newTester(a)
	settleLoads(t, tt, a, q)
	click(t, tt, "Library")
	click(t, tt, "Albums")
	click(t, tt, "Velvet Static")
	wantPath(t, a, albumPath("velvet-static"))
	// Album pages omit the album column; "ALBUM" is only the eyebrow.
	if !tt.HasText("Static Bloom") || count(tt.Texts(), "ALBUM") != 1 {
		t.Errorf("album page wrong, texts %q", tt.Texts())
	}
}

func TestSearch(t *testing.T) {
	a, q := testApp(t)
	tt := newTester(a)
	tt.Key(ui.Cmd, ui.KeyK)
	if !tt.Focused("Search") {
		t.Fatal("Cmd+K left the search field unfocused")
	}
	tt.Type("orb")
	tt.Type("it")
	settleLoads(t, tt, a, q)
	wantPath(t, a, searchPath("orbit"))
	if !tt.HasText("Top result") || !tt.HasText("Gravity Well") {
		t.Errorf("no results for orbit, texts %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	settleLoads(t, tt, a, q)
	if a.search.query != "" || !tt.HasText("Quick picks") {
		t.Errorf("Escape left query %q", a.search.query)
	}
	wantPath(t, a, pathHome)

	a.router.Push(searchPath("zzz"))
	settleLoads(t, tt, a, q)
	if a.search.query != "zzz" || !tt.HasText("No results for “zzz”") {
		t.Errorf("query %q, texts %q", a.search.query, tt.Texts())
	}
}

func TestSearchHistory(t *testing.T) {
	a, q := testApp(t)
	tt := newTester(a)
	click(t, tt, "Explore")
	click(t, tt, "Search")
	tt.Type("o")
	tt.Type("rbit")
	tt.Frame()
	// Typing replaces the search page, so one Back leaves search.
	click(t, tt, "Back")
	wantPath(t, a, pathExplore)
	if a.search.query != "" {
		t.Errorf("query %q after leaving search", a.search.query)
	}
	click(t, tt, "Forward")
	wantPath(t, a, searchPath("orbit"))
	if a.search.query != "orbit" {
		t.Errorf("query %q after returning to search", a.search.query)
	}
	settleLoads(t, tt, a, q)
	click(t, tt, "Low Orbit")
	wantPath(t, a, albumPath("low-orbit"))
	if a.search.query != "" {
		t.Errorf("query %q on an album", a.search.query)
	}
}

func TestPanelToggles(t *testing.T) {
	a := idleApp(t)
	tt := newTester(a)
	click(t, tt, "Lyrics")
	if !a.panel.isShown || a.panel.tab != tabLyrics {
		t.Fatalf("lyrics tab not shown: panel %v tab %d", a.panel.isShown, a.panel.tab)
	}
	click(t, tt, "Hide panel")
	if a.panel.isShown {
		t.Fatal("panel still open")
	}
	// Reopen it mid-close, then close it mid-open.
	tt.Advance(2 * frameTime)
	click(t, tt, "Queue")
	tt.Advance(2 * frameTime)
	if !a.panel.isShown || a.panel.tab != tabQueue {
		t.Fatalf("panel %v tab %d after reopening it", a.panel.isShown, a.panel.tab)
	}
	settle(t, tt, "panel opening", func() bool {
		r, ok := tt.Find("Hide panel")
		return ok && r.X+r.W < 1360-a.theme.Layout.Gutter
	})
	click(t, tt, "Hide panel")
	settle(t, tt, "panel closing", func() bool {
		return !tt.HasText("Lyrics placeholder") && !tt.HasText("From Neon Tide")
	})
	if _, ok := tt.Find("Hide panel"); ok {
		t.Error("panel still built once hidden")
	}
}

func TestOverComposites(t *testing.T) {
	hover := theme.Default().Color.GhostHover
	if got := over(hover, ui.Transparent); got != hover {
		t.Errorf("over nothing %v, want %v", got, hover)
	}
	got := over(ui.RGBA(255, 255, 255, 0.5), ui.RGB(0, 0, 0))
	if got.A != 255 || got.R < 127 || got.R > 128 {
		t.Errorf("half white over black %v", got)
	}
}

func frames(tt *ui.Tester, n int) {
	for range n {
		tt.Frame()
	}
}

// frameTime is a display frame at 60 Hz, the Tester's refresh.
const frameTime = time.Second / 60

// settle moves the Tester's clock a frame at a time, up to a second:
// it stands still otherwise, so a motion would never end.
func settle(t *testing.T, tt *ui.Tester, what string, done func() bool) {
	t.Helper()
	for end := tt.Now().Add(time.Second); !done(); {
		if !tt.Now().Before(end) {
			t.Fatalf("%s did not happen, texts %q", what, tt.Texts())
		}
		tt.Advance(frameTime)
	}
}

// nowPlayingAnnouncements consumes the announcements made since the last
// read.
func nowPlayingAnnouncements(tt *ui.Tester) []string {
	var out []string
	for _, s := range tt.Announcements() {
		if strings.HasPrefix(s, "Now playing: ") {
			out = append(out, s)
		}
	}
	return out
}

func TestEveryPage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(a *app)
		want  []string
	}{
		{"home", func(*app) {}, []string{
			"Listen now", "Quick picks", "Albums for you", "Mixed for you", "Artists you love", "My Supermix", "Up next",
		}},
		{"explore", func(a *app) { a.router.Push(pathExplore) }, []string{"Moods & genres", "Trending"}},
		{"library", func(a *app) { a.router.Push(libraryPath(tabPlaylists)) }, []string{"Liked music", "Late Night Drive"}},
		{"library songs", func(a *app) { a.router.Push(libraryPath(tabSongs)) }, []string{"TITLE", "ALBUM"}},
		{"library albums", func(a *app) { a.router.Push(libraryPath(tabAlbums)) }, []string{"Velvet Static"}},
		{
			"library artists", func(a *app) { a.router.Push(libraryPath(tabArtists)) },
			[]string{"Lumen Drive", "Artist"},
		},
		{"album", func(a *app) { a.router.Push(albumPath("velvet-static")) }, []string{"Static Bloom"}},
		{
			"playlist", func(a *app) { a.router.Push(playlistPath("late-night-drive")) },
			[]string{"TITLE", "Midnight Arcade"},
		},
		{"liked", func(a *app) { a.router.Push(pathLiked) }, []string{"Auto playlist", "Gravity Well"}},
		{"liked empty", func(a *app) { a.session.KeepLikes(nil); a.router.Push(pathLiked) }, []string{"Nothing here yet"}},
		{"search", func(a *app) { a.router.Push(searchPath("orbit")) }, []string{"Top result", "Gravity Well"}},
		{"search empty", func(a *app) { a.router.Push(searchPath("zzz")) }, []string{"No results for “zzz”"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, q := testApp(t)
			tc.setup(a)
			tt := newTester(a)
			settleLoads(t, tt, a, q)
			frames(tt, 3)
			wantTexts(t, tt, tc.want...)
		})
	}
}

func TestLikedEmptiesAndReturns(t *testing.T) {
	a := idleApp(t)
	current := a.currentID()
	a.session.KeepLikes([]string{current})
	a.router.Push(pathLiked)
	tt := newTester(a)
	// The row's Like is found first: the page builds before the panel.
	click(t, tt, "Like")
	frames(tt, 2)
	isEmpty := tt.HasText("Nothing here yet") && !tt.HasText("TITLE")
	if a.session.Liked(current) || !isEmpty {
		t.Fatalf("unliking the last song left liked %v, texts %q", a.session.Liked(current), tt.Texts())
	}
	// Now the panel's Like, for the same song.
	click(t, tt, "Like")
	frames(tt, 2)
	isListed := !tt.HasText("Nothing here yet") && tt.HasText("TITLE")
	if !a.session.Liked(current) || !isListed {
		t.Errorf("liking it again left liked %v, texts %q", a.session.Liked(current), tt.Texts())
	}
}

func TestPlaybackShortcuts(t *testing.T) {
	a := idleApp(t)
	tt := newTester(a)
	songs := a.cat.SongIDs()
	tt.Key(ui.Cmd, ui.KeyRight)
	if got := state(t, a).Current; got != songs[1] {
		t.Fatalf("Cmd+Right went to %q, want %q", got, songs[1])
	}
	a.client.Seek(10)
	settled(t, a)
	tt.Key(ui.Cmd, ui.KeyLeft)
	if st := state(t, a); st.Current != songs[1] || st.Position != 0 {
		t.Fatalf("Cmd+Left 10s in went to %q at %v, want a restart", st.Current, st.Position)
	}
	tt.Key(ui.Cmd, ui.KeyLeft)
	if got := state(t, a).Current; got != songs[0] {
		t.Errorf("Cmd+Left at the start went to %q, want %q", got, songs[0])
	}
}

func TestSpaceAfterClickingNext(t *testing.T) {
	a := idleApp(t)
	tt := newTester(a)
	click(t, tt, "Next")
	if tt.Focused("Next") {
		t.Error("clicking Next took the focus")
	}
	playing := state(t, a).Playing
	tt.Key(0, ui.KeySpace)
	if state(t, a).Playing == playing {
		t.Error("Space after clicking Next did not play or pause")
	}
	// Tab still reaches the transport.
	transport := []string{"Next", "Previous", "Shuffle", "Play", "Pause"}
	for range 40 {
		if slices.ContainsFunc(transport, tt.Focused) {
			return
		}
		tt.Key(0, ui.KeyTab)
	}
	t.Error("Tab never reached the transport")
}

func TestRowActionsDoNotPlayTheRow(t *testing.T) {
	a := idleApp(t)
	tt := newTester(a)
	before := a.currentID()
	r, ok := tt.Find("Paper Moons by Ivy Calder")
	if !ok {
		t.Fatal("no Paper Moons row")
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	// Hovering shows the row's actions and narrows the song's button.
	r, _ = tt.Find("Paper Moons by Ivy Calder")
	// Like follows the song's button, past the row's gap of 14.
	tt.ClickAt(r.X+r.W+14+16, r.Y+r.H/2)
	if !a.session.Liked("paper-moons") {
		t.Fatal("the row's Like did not like the song")
	}
	if st := state(t, a); st.Current != before || st.Playing {
		t.Errorf("the row's Like played %q (playing %v)", st.Current, st.Playing)
	}
}

func TestSliderKeys(t *testing.T) {
	a := idleApp(t)
	tt := newTester(a)
	click(t, tt, "Seek")
	a.client.Seek(100)
	settled(t, a)
	tt.Frame()
	tt.Key(0, ui.KeyRight)
	if got := state(t, a).Position; got != 100+seekStep {
		t.Errorf("Right moved the song from 100 to %v, want %v", got, 100+seekStep)
	}
	tt.Frame()
	tt.Key(0, ui.KeyLeft)
	tt.Key(0, ui.KeyLeft)
	if got := state(t, a).Position; got != 100-seekStep {
		t.Errorf("Left twice moved it to %v, want %v", got, 100-seekStep)
	}
	click(t, tt, "Mute")
	click(t, tt, "Volume")
	settled(t, a)
	tt.Frame()
	tt.Key(0, ui.KeyRight)
	if st := state(t, a); st.Muted || math.Abs(st.Volume-(0.5+volumeStep)) > 1e-9 {
		t.Errorf("Right on the volume left muted %v at %v", st.Muted, st.Volume)
	}
}

func TestNarrowWindow(t *testing.T) {
	a := idleApp(t)
	tt := newTester(a)
	first, second := "Midnight Arcade by Lumen Drive", "Satellite Hearts by Lumen Drive"
	sameRow := func() bool {
		r1, ok1 := tt.Find(first)
		r2, ok2 := tt.Find(second)
		if !ok1 || !ok2 {
			t.Fatalf("quick picks missing, texts %q", tt.Texts())
		}
		return r1.Y == r2.Y
	}
	if !sameRow() || !tt.HasText(hints.search) {
		t.Fatal("a wide window should show quick picks in columns and the search shortcut")
	}
	tt.SetSize(1040, 680)
	frames(tt, 2)
	if sameRow() {
		t.Error("quick picks keep several columns at 1040 wide")
	}
	if tt.HasText(hints.search) {
		t.Error("the narrow search field keeps the shortcut hint")
	}
}

func TestHiDPI(t *testing.T) {
	tt := newTester(idleApp(t))
	tt.SetScale(2)
	frames(tt, 2)
	if w := tt.Image().Bounds().Dx(); w != 2720 {
		t.Errorf("image %d pixels wide at scale 2, want 2720", w)
	}
	if !tt.HasText("Quick picks") {
		t.Errorf("home missing at scale 2, texts %q", tt.Texts())
	}
}

func TestStaysDarkInLightMode(t *testing.T) {
	tt := newTester(idleApp(t))
	tt.SetDark(false)
	frames(tt, 2)
	px := tt.Image().RGBAAt(700, 3)
	if px.R > 30 || px.G > 30 || px.B > 30 {
		t.Errorf("window background %v in light mode, want dark", px)
	}
}

func TestReduceMotion(t *testing.T) {
	for _, reduce := range []bool{false, true} {
		a := idleApp(t)
		builds := 0
		tt := ui.NewTester(func(c *ui.Context) { builds++; a.view(c) }, 1360, 860)
		tt.SetPreferences(ui.Preferences{ReduceMotion: reduce})
		frames(tt, 3)
		builds = 0
		a.panel.isShown = false
		tt.Frame()
		// The slide builds every frame; without it, hiding takes one build.
		if snapped := builds == 1; snapped != reduce {
			t.Errorf("reduce motion %v: hiding the panel took %d builds", reduce, builds)
		}
		if _, ok := tt.Find("Hide panel"); reduce && ok {
			t.Error("the panel is still shown with reduced motion")
		}
	}
}

func TestHighContrast(t *testing.T) {
	a := idleApp(t)
	tt := newTester(a)
	tt.SetPreferences(ui.Preferences{HighContrast: true})
	usesContrast := th == &a.contrast &&
		th.Color.Muted == a.theme.Contrast.Muted &&
		th.Color.Focus == a.theme.Contrast.Focus
	if !usesContrast {
		t.Error("High Contrast left the standard theme")
	}
	tt.SetPreferences(ui.Preferences{})
	if th != &a.theme || th.Color.Muted != theme.Default().Color.Muted {
		t.Error("turning High Contrast off kept its theme")
	}
}

func TestAnnouncesSongChanges(t *testing.T) {
	a := idleApp(t)
	tt := newTester(a)
	if got := nowPlayingAnnouncements(tt); len(got) != 0 {
		t.Errorf("announced %q at start", got)
	}
	want := func(action, s string) {
		t.Helper()
		settled(t, a)
		tt.Frame()
		if got := nowPlayingAnnouncements(tt); len(got) != 1 || got[0] != s {
			t.Errorf("%s announced %q, want just %q", action, got, s)
		}
	}
	click(t, tt, "Next")
	want("Next", "Now playing: Satellite Hearts by Lumen Drive")
	tt.Key(0, ui.KeySpace)
	settled(t, a)
	tt.Frame()
	if got := nowPlayingAnnouncements(tt); len(got) != 0 {
		t.Errorf("pausing announced %q", got)
	}
	tt.Key(ui.Cmd, ui.KeyRight)
	want("Cmd+Right", "Now playing: Paper Moons by Ivy Calder")
	click(t, tt, "Midnight Arcade by Lumen Drive")
	want("a row", "Now playing: Midnight Arcade by Lumen Drive")
}

// testLocale is the language tests show; see TestMain.
var testLocale = "en"

// inLanguage shows lang until the test ends.
func inLanguage(t *testing.T, lang string) {
	t.Helper()
	testLocale = lang
	t.Cleanup(func() { testLocale = "en" })
}
