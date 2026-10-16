package main

import (
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"weak"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/fetchstate"
	"yt-music-player/internal/loader"
	"yt-music-player/internal/theme"

	"github.com/egoist/mygo/ui"
)

// pageCases are BenchmarkPages' screens, each set up on a fresh app.
var pageCases = []struct {
	name  string
	setup func(a *app)
}{
	{"Home", func(*app) {}},
	{"Explore", func(a *app) { a.router.Push(pathExplore) }},
	{"Library/Playlists", func(a *app) { a.router.Push(libraryPath(tabPlaylists)) }},
	{"Library/Songs", func(a *app) { a.router.Push(libraryPath(tabSongs)) }},
	{"Library/Albums", func(a *app) { a.router.Push(libraryPath(tabAlbums)) }},
	{"Library/Artists", func(a *app) { a.router.Push(libraryPath(tabArtists)) }},
	{"Album", func(a *app) { a.router.Push(albumPath(a.cat.Albums[0].ID)) }},
	{"Playlist", func(a *app) { a.router.Push(playlistPath(a.cat.Playlists[0].ID)) }},
	{"LikedMusic", func(a *app) { a.router.Push(pathLiked) }},
	// No debounce: it would only slow the setup.
	{"Search", func(a *app) {
		a.loader.Debounce = 0
		a.router.Push(searchPath("lumen"))
	}},
	{"Home/PanelHidden", func(a *app) { a.panel.isShown = false }},
	// The current song's equalizer should paint without building the view.
	{"Home/Playing", func(a *app) { a.player.Playing = true }},
	{"Library/Songs/2000", func(a *app) {
		useCatalog(a, bigCatalog())
		a.router.Push(libraryPath(tabSongs))
	}},
	// Every cover a loaded thumbnail (see thumbnailCatalog).
	{"Library/Albums/Thumbnails", func(a *app) {
		useCatalog(a, thumbnailCatalog())
		a.router.Push(libraryPath(tabAlbums))
	}},
	// A Home of YouTube Music's size, beside a library of 40 playlists.
	{"Home/Feed", useFeed},
	{"Playlist/2000", func(a *app) {
		useCatalog(a, bigCatalog())
		big := catalog.Playlist{ID: "big", Name: "Everything", Owner: "Lan", Art: "neon-tide", Songs: a.cat.SongIDs()}
		a.cat.Add(nil, nil, []catalog.Playlist{big})
		a.router.Push(playlistPath(big.ID))
		a.fetches.Set(
			fetchstate.Resource{Kind: fetchstate.Playlist, ID: big.ID},
			fetchstate.Fetch{State: fetchstate.Loaded},
		)
	}},
	{"Home/Appearance", func(a *app) { a.openLook() }},
	// Every build of the sheet checks the pasted header.
	{"Home/SignIn", func(a *app) { a.sheet.isOpen, a.sheet.credentials = true, pastedCookie }},
	{"LikedMusic/2000", func(a *app) {
		useCatalog(a, bigCatalog())
		a.session.KeepLikes(a.cat.SongIDs())
		a.router.Push(pathLiked)
	}},
}

// pastedCookie is a Cookie header the size of YouTube Music's, pasted
// over several lines as a browser's developer tools copy it.
var pastedCookie = func() string {
	var b strings.Builder
	b.WriteString("Cookie: ")
	for i := range 40 {
		b.WriteString("NAME" + strconv.Itoa(i) + "=" + strings.Repeat("v", 40) + ";")
		sep := " "
		if i%8 == 7 {
			sep = "\n "
		}
		b.WriteString(sep)
	}
	return b.String()
}()

// feedHome is a Home of the size YouTube Music sends, each item with its
// own thumbnailServer art.
func feedHome() catalog.Home {
	itoa := strconv.Itoa
	art := func(kind string, i int) catalog.Art { return thumbArt(catalog.Art(kind + "-" + itoa(i))) }
	song := func(i int) catalog.Item {
		return catalog.Item{Kind: catalog.ItemSong, Song: catalog.Song{
			ID: "feed-song-" + itoa(i), Title: "Feed Song " + itoa(i), Artist: "Feed Artist " + itoa(i%7),
			Album: "Feed Album " + itoa(i%5), Seconds: 150 + i, Art: art("song", i),
		}}
	}
	album := func(i int) catalog.Item {
		return catalog.Item{Kind: catalog.ItemAlbum, Album: catalog.Album{
			ID: "MPREb_feed" + itoa(i), Title: "Feed Album " + itoa(i), Artist: "Feed Artist " + itoa(i%7),
			Year: 2000 + i, Kind: catalog.KindAlbum, Art: art("album", i),
		}}
	}
	playlist := func(i int) catalog.Item {
		return catalog.Item{Kind: catalog.ItemPlaylist, Playlist: catalog.Playlist{
			ID: "RDfeed" + itoa(i), Name: "Feed Mix " + itoa(i), Owner: "YouTube Music", Art: art("playlist", i),
		}}
	}
	artist := func(i int) catalog.Item {
		return catalog.Item{Kind: catalog.ItemArtist, Artist: catalog.Artist{
			ID: "UCfeed" + itoa(i), Name: "Feed Artist " + itoa(i), Art: art("artist", i),
		}}
	}
	shelf := func(title string, count int, item func(int) catalog.Item) catalog.Shelf {
		sh := catalog.Shelf{Title: title}
		for i := range count {
			sh.Items = append(sh.Items, item(i))
		}
		return sh
	}
	return catalog.Home{Shelves: []catalog.Shelf{
		shelf("Quick picks", 20, song),
		shelf("Albums for you", 16, func(i int) catalog.Item { return album(i) }),
		shelf("Mixed for you", 12, playlist),
		shelf("Artists you might like", 14, artist),
		shelf("New releases", 18, func(i int) catalog.Item { return album(100 + i) }),
		shelf("From the community", 10, func(i int) catalog.Item {
			switch i % 3 {
			case 0:
				return song(100 + i)
			case 1:
				return album(200 + i)
			}
			return playlist(100 + i)
		}),
	}}
}

func useFeed(a *app) {
	a.loader.KeepHome(feedHome())
	var lib catalog.Library
	for i := range 40 {
		lib.Playlists = append(lib.Playlists, catalog.Playlist{
			ID: "PLlib" + strconv.Itoa(i), Name: "Library Playlist " + strconv.Itoa(i), Owner: "Lan",
			Art: thumbArt(catalog.Art("library-" + strconv.Itoa(i))),
		})
	}
	a.session.KeepAccount(catalog.Account{Name: "Lan"}, lib)
	a.session.KeepLikes(catalog.PlaceholderLikes())
}

const bigSongs = 2000

func bigCatalog() *catalog.Catalog {
	small := catalog.Placeholder()
	songs := make([]catalog.Song, bigSongs)
	for i := range songs {
		songs[i] = small.Songs[i%len(small.Songs)]
		if i >= len(small.Songs) {
			songs[i].ID += "-" + strconv.Itoa(i)
		}
	}
	big := catalog.New(songs, small.Albums, small.Playlists)
	big.SetLibrary(libraryIDs(small))
	return big
}

func libraryIDs(cat *catalog.Catalog) []string {
	var ids []string
	for _, pl := range cat.LibraryPlaylists() {
		ids = append(ids, pl.ID)
	}
	return ids
}

// useCatalog also keeps cat as the account's library, so the source's
// songs do not replace cat's.
func useCatalog(a *app, cat *catalog.Catalog) {
	*a.cat = *cat
	a.session.KeepAccount(catalog.Account{Name: "Lan"}, catalog.Library{
		Playlists: slices.Clone(cat.LibraryPlaylists()),
		Albums:    slices.Clone(cat.Albums),
		Artists:   catalog.AlbumArtists(cat.Albums),
		Songs:     slices.Clone(cat.Songs),
	})
	a.session.KeepLikes(catalog.PlaceholderLikes())
}

// screen is an app shown on a Tester, as benchmarks and budgets measure it.
type screen struct {
	a      *app
	q      posts
	tt     *ui.Tester
	builds int
}

// newScreen waits for fetches: pages are measured loaded, not loading.
func newScreen(tb testing.TB, setup func(a *app)) *screen {
	tb.Helper()
	a, q := testApp()
	setup(a)
	return showApp(tb, a, q)
}

func showApp(tb testing.TB, a *app, q posts) *screen {
	tb.Helper()
	s := &screen{a: a, q: q}
	s.tt = ui.NewTester(func(c *ui.Context) { s.builds++; a.view(c) }, 1360, 860)
	s.tt.SetTitleBar(ui.TitleBar{Height: a.theme.Layout.TitleBar, Left: 78})
	settleLoads(tb, s.tt, a, q)
	return s
}

// steady draws frames until the view stops animating and caches are warm:
// a thumbnail's color eases the glow in for a while.
func (s *screen) steady() {
	for range 5 {
		s.tt.Frame()
	}
	// The Tester's clock stands still until moved: motions end only so.
	for end := s.tt.Now().Add(5 * time.Second); s.tt.FramePending() && s.tt.Now().Before(end); {
		s.tt.Advance(100 * time.Millisecond)
	}
}

// retainedKB drops *s and returns the heap it kept reachable: the heap with
// it, less the heap once it is collected. The heap before it was made would
// also count what earlier screens left to collect and the shared caches it
// warmed, so a page would measure differently alone and after others.
func retainedKB(tb testing.TB, s **screen) int64 {
	tb.Helper()
	with := liveHeap()
	app := weak.Make((*s).a)
	// The app being built stays reachable through th and thumbs until
	// another view runs.
	*s, th, thumbs = nil, new(theme.Default()), nil
	// The Tester arms no real timers (its clock is the test's), so a
	// collection or two lets it go; the wait guards against a leak.
	for wait := time.Now().Add(2 * time.Second); app.Value() != nil; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(wait) {
			tb.Fatal("the dropped screen stays reachable")
		}
		runtime.GC()
	}
	return (with - liveHeap()) / 1024
}

// BenchmarkPages measures one steady frame per page. ns/op covers build,
// layout and scene recording (the Tester rasterizes only on Image); the
// Tester settles up to 20 frames while the view animates; live-KB is the heap
// the screen retains (retainedKB). The Tester adds checks a release build
// skips, so compare pages with each other, not with the app.
func BenchmarkPages(b *testing.B) {
	for _, pc := range pageCases {
		b.Run(pc.name, func(b *testing.B) {
			s := newScreen(b, pc.setup)
			s.steady()
			b.ReportAllocs()
			s.builds = 0
			for b.Loop() {
				s.tt.Frame()
			}
			b.StopTimer()
			b.ReportMetric(float64(s.builds)/float64(b.N), "builds/op")
			b.ReportMetric(float64(retainedKB(b, &s)), "live-KB")
		})
	}
}

// interactionCases are BenchmarkInteractions' inputs: start readies the
// loaded page and returns the op, called with i counting from 0.
var interactionCases = []struct {
	name  string
	setup func(a *app)
	start func(tb testing.TB, s *screen) func(i int)
}{
	// The pointer moves from one song row to the next.
	{"HoverRows", func(a *app) {
		useCatalog(a, bigCatalog())
		a.router.Push(libraryPath(tabSongs))
	}, func(tb testing.TB, s *screen) func(int) {
		ys := rowsAt(tb, s.tt, s.a.cat.Songs[:8])
		return func(i int) { s.tt.Move(700, ys[i%len(ys)]) }
	}},
	// The pointer moves from one card of a Home shelf to the next.
	{"HoverCards", useFeed, func(tb testing.TB, s *screen) func(int) {
		items := s.a.loader.Home().Shelves[1].Items[:5]
		find := func(it catalog.Item) ui.Rect {
			r, ok := s.tt.Find(it.Album.Title)
			if !ok {
				tb.Fatalf("no card %q in %q", it.Album.Title, s.tt.Texts())
			}
			return r
		}
		// Below the hero and the quick picks: scroll them into view.
		s.tt.Scroll(700, 500, 0, find(items[0]).Y-400)
		var xs []float32
		var y float32
		for _, it := range items {
			r := find(it)
			xs, y = append(xs, r.X+r.W/2), r.Y+r.W/2
		}
		if y < 100 || y > 760 {
			tb.Fatalf("cards at %v, out of view", y)
		}
		return func(i int) { s.tt.Move(xs[i%len(xs)], y) }
	}},
	// The pointer goes onto the play button of the player bar and off it.
	{"HoverPlay", func(*app) {}, func(tb testing.TB, s *screen) func(int) {
		prev, ok1 := s.tt.Find("Previous")
		next, ok2 := s.tt.Find("Next")
		if !ok1 || !ok2 {
			tb.Fatalf("no transport in %q", s.tt.Texts())
		}
		x, y := (prev.X+prev.W+next.X)/2, prev.Y+prev.H/2
		return func(i int) { s.tt.Move(x, y-float32(i%2)*60) }
	}},
	// The pointer goes on and off the hero's thumbnail sleeves, which fan.
	{"HoverHero", useFeed, hoverHero},
	// The placeholder's painted sleeves, some with motifs that clip.
	{"HoverHero/Painted", func(*app) {}, hoverHero},
	// A wheel scrolls 2000 songs down a step at a time, and back up.
	{"ScrollSongs", func(a *app) {
		useCatalog(a, bigCatalog())
		a.router.Push(libraryPath(tabSongs))
	}, func(tb testing.TB, s *screen) func(int) {
		list := &s.a.page.library[tabSongs]
		dy := float32(-120)
		s.tt.Scroll(700, 500, 0, dy)
		if first, _ := list.Visible(); first == 0 {
			dy = -dy
		}
		return func(i int) {
			// 400 steps of 120 DIPs reach about row 800 of 2000.
			step := dy
			if i/400%2 == 1 {
				step = -dy
			}
			s.tt.Scroll(700, 500, 0, step)
			if i == 399 {
				if first, _ := list.Visible(); first < 400 {
					tb.Fatalf("scrolled to row %d", first)
				}
			}
		}
	}},
	// Typing alternates between a query with results shown and one being
	// searched for.
	{"TypeSearch", func(a *app) { a.loader.Debounce = 0 }, func(tb testing.TB, s *screen) func(int) {
		s.tt.Key(ui.Cmd, ui.KeyK)
		s.tt.Type("lumen")
		settleLoads(tb, s.tt, s.a, s.q)
		if f := s.a.loader.Found(); f.Query != "lumen" || f.Results.IsEmpty() {
			tb.Fatalf("searched for %q, found %+v", f.Query, f.Results)
		}
		s.a.loader.Debounce = loader.SearchDebounce
		return func(i int) {
			drain(s.q)
			if i%2 == 0 {
				s.tt.Key(0, ui.KeyBackspace)
			} else {
				s.tt.Type("n")
			}
		}
	}},
	{"LibraryTabs", func(a *app) {
		useCatalog(a, thumbnailCatalog())
		a.router.Push(libraryPath(tabPlaylists))
	}, func(tb testing.TB, s *screen) func(int) {
		// Visit each tab first so the loop finds its thumbnails loaded.
		for tab := range libraryTabs {
			s.a.router.Replace(libraryPath(libraryTab(tab)))
			settleLoads(tb, s.tt, s.a, s.q)
		}
		return func(i int) {
			s.a.router.Replace(libraryPath(libraryTab((1 + i) % len(libraryTabs))))
			s.tt.Frame()
		}
	}},
}

// hoverHero moves the pointer between the hero's front sleeve and its copy.
func hoverHero(tb testing.TB, s *screen) func(int) {
	open, ok := s.tt.Find("Open")
	if !ok {
		tb.Fatalf("no hero in %q", s.tt.Texts())
	}
	// The sleeves are the hero's last 300 DIPs, left of the panel.
	layout := &th.Layout
	_, right := gutters()
	x := 1360 - right - (layout.Panel+layout.Gutter)*boolToFloat(s.a.panel.isShown) - layout.Page - 150
	y := open.Y + open.H/2
	return func(i int) {
		if i%2 == 0 {
			s.tt.Move(x, y)
		} else {
			s.tt.Move(open.X+open.W+40, y)
		}
	}
}

func rowsAt(tb testing.TB, tt *ui.Tester, songs []catalog.Song) []float32 {
	tb.Helper()
	var ys []float32
	for _, s := range songs {
		r, ok := tt.Find(s.Title)
		if !ok {
			tb.Fatalf("no row %q in %q", s.Title, tt.Texts())
		}
		ys = append(ys, r.Y+r.H/2)
	}
	return ys
}

func drain(q posts) {
	for {
		select {
		case fn := <-q:
			fn()
		default:
			return
		}
	}
}

func interacting(tb testing.TB, setup func(a *app), start func(testing.TB, *screen) func(int)) (*screen, func(int)) {
	tb.Helper()
	s := newScreen(tb, setup)
	op := start(tb, s)
	settleLoads(tb, s.tt, s.a, s.q)
	// Otherwise the first op shares its frames with the glow easing to the
	// thumbnails' colors.
	s.steady()
	return s, op
}

// BenchmarkInteractions measures an input plus the frames it settles (20
// while it animates, as the Tester's clock stands still between ops);
// builds/op counts view builds, live-KB is as BenchmarkPages'.
func BenchmarkInteractions(b *testing.B) {
	for _, ic := range interactionCases {
		b.Run(ic.name, func(b *testing.B) {
			s, op := interacting(b, ic.setup, ic.start)
			b.ReportAllocs()
			s.builds = 0
			i := 0
			for b.Loop() {
				op(i)
				i++
			}
			b.StopTimer()
			b.ReportMetric(float64(s.builds)/float64(b.N), "builds/op")
			b.ReportMetric(float64(retainedKB(b, &s)), "live-KB")
		})
	}
}

func liveHeap() int64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int64(m.HeapAlloc)
}

// Sinks keep the compiler from optimizing measured calls away.
var (
	sinkString     string
	sinkInt        int
	sinkBool       bool
	sinkCover      cover
	sinkResource   fetchstate.Resource
	sinkArt        catalog.Art
	sinkSong       catalog.Song
	sinkCollection collection
	sinkSideItems  []sideItem
	sinkCards      [][]cardText
	sinkCardRow    []cardText
	sinkKeys       []any
	sinkTyped      *typedText
	sinkFound      *foundText
	sinkRowText    *rowText
)

// helperApp is an app as helpers find it in a build: a large library, all
// liked, a full-size Home, Explore fetched, on an album page.
func helperApp() *app {
	thumbs = nil
	a := idleApp()
	// Before useCatalog replaces the catalog the feed's music joins.
	a.loader.KeepHome(feedHome())
	useCatalog(a, bigCatalog())
	a.session.KeepLikes(a.cat.SongIDs())
	a.router.Push(albumPath("neon-tide"))
	return a
}

// helperCases are view helpers run on a helperApp, i counting the calls;
// limit is the allocations a call may make.
var helperCases = []struct {
	name  string
	limit float64
	call  func(a *app, i int)
}{
	// Limits are today's allocs but where a comment says.
	{"Clock/cached", 0, func(*app, int) { sinkString = clock(227) }},
	{"Clock/over_an_hour", 1, func(*app, int) { sinkString = clock(3725) }},
	// Covers: a placeholder name, a thumbnail URL (whose hash picks the
	// colors while it loads), and other art.
	{"CoverOf/named", 0, func(*app, int) { sinkCover = coverOf("neon-tide") }},
	{"CoverOf/url", 0, func(*app, int) {
		sinkCover = coverOf("https://lh3.googleusercontent.com/abc_DEF-123=w544-h544-l90-rj")
	}},
	{"CoverOf/other", 0, func(*app, int) { sinkCover = coverOf("unknown") }},
	{"ColumnsFor", 0, func(*app, int) { sinkInt = moodColumns(1012) }},
	{"Greeting", 0, func(*app, int) { sinkString = greeting(time.Date(2026, 5, 1, 14, 0, 0, 0, time.UTC)) }},
	// Plural messages go through ICU formatting (locales), which allocates
	// about ten times a count; they are made again only as music changes.
	{"TotalLength/18", 23, func(a *app, _ int) { sinkString = totalLength(a.cat.Songs[:18]) }}, // 20 allocs today.
	{"TotalLength/2000", 28, func(a *app, _ int) { sinkString = totalLength(a.cat.Songs) }},    // 24 allocs today.
	{"Upper", 0, func(*app, int) { sinkString = upper("Auto playlist") }},
	{"PlaylistLine/count", 17, func(a *app, _ int) { sinkString = playlistLine(a.cat.Playlists[0], true) }}, // 14 allocs today: plural.
	{"PlaylistLine/owner", 1, func(a *app, _ int) { sinkString = playlistLine(a.cat.Playlists[0], false) }},
	{"Percent", 0, func(*app, int) { sinkString = percent(70) }},
	// Table rows' positions, cached as far as playlists go.
	{"Numeral/small", 0, func(*app, int) { sinkString = numeral(7) }},
	{"Numeral/large", 0, func(*app, int) { sinkString = numeral(1234) }},
	{"Numeral/past_cache", 1, func(*app, int) { sinkString = numeral(12345) }},
	// The seek bar's screen-reader value is reused while the second stays.
	{"SeekValue/same", 0, func(a *app, _ int) { sinkString = a.bar.seekValue(84, 227) }},
	// A second not shown before also fills clock's cache, in a build the time
	// label's cost: the position alternates between two the first call made.
	{"SeekValue/changed", 1, func(a *app, i int) { sinkString = a.bar.seekValue(i%2*227, 227) }},
	// Locations of pages that fetch, with an escaped ID, and that don't.
	{"ResourceAt/album", 1, func(*app, int) { sinkResource = resourceAt("/album/MPREb_7ltM34kr0mH") }},
	// 5 allocs today.
	{"ResourceAt/playlist_escaped", 6, func(*app, int) { sinkResource = resourceAt("/playlist/PL%2Fwith%20slash") }},
	{"ResourceAt/home", 1, func(*app, int) { sinkResource = resourceAt(pathHome) }},
	{"ResourceAt/search", 1, func(*app, int) { sinkResource = resourceAt("/search?q=daft+punk") }},
	// The view asks for the path many times a build: parsed once per location.
	{"Path/hit", 0, func(a *app, _ int) { sinkString = a.path() }},
	{"Path/miss", 4, func(a *app, _ int) { // 3 allocs today: the location parsed.
		a.memo.loc.isSet = false
		sinkString = a.path()
	}},
	{"OnPath", 0, func(a *app, _ int) { sinkBool = a.onPath(pathLibrary + "/") }},
	{"GlowArt", 0, func(a *app, _ int) { sinkArt = a.glowArt() }},
	{"CurrentSong", 0, func(a *app, _ int) { sinkSong = a.currentSong() }},
}

// benchHelper runs fn's helperCases: the one named fn, or each fn/case as a
// sub-benchmark.
func benchHelper(b *testing.B, fn string) {
	a := helperApp()
	for _, tc := range helperCases {
		bench := func(b *testing.B) {
			tc.call(a, 0)
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				tc.call(a, i)
				i++
			}
		}
		if tc.name == fn {
			bench(b)
			continue
		}
		if sub, ok := strings.CutPrefix(tc.name, fn+"/"); ok {
			b.Run(sub, bench)
		}
	}
}

func BenchmarkClock(b *testing.B)        { benchHelper(b, "Clock") }
func BenchmarkCoverOf(b *testing.B)      { benchHelper(b, "CoverOf") }
func BenchmarkColumnsFor(b *testing.B)   { benchHelper(b, "ColumnsFor") }
func BenchmarkGreeting(b *testing.B)     { benchHelper(b, "Greeting") }
func BenchmarkTotalLength(b *testing.B)  { benchHelper(b, "TotalLength") }
func BenchmarkUpper(b *testing.B)        { benchHelper(b, "Upper") }
func BenchmarkPlaylistLine(b *testing.B) { benchHelper(b, "PlaylistLine") }
func BenchmarkPercent(b *testing.B)      { benchHelper(b, "Percent") }
func BenchmarkNumeral(b *testing.B)      { benchHelper(b, "Numeral") }
func BenchmarkSeekValue(b *testing.B)    { benchHelper(b, "SeekValue") }
func BenchmarkResourceAt(b *testing.B)   { benchHelper(b, "ResourceAt") }
func BenchmarkPath(b *testing.B)         { benchHelper(b, "Path") }
func BenchmarkOnPath(b *testing.B)       { benchHelper(b, "OnPath") }
func BenchmarkGlowArt(b *testing.B)      { benchHelper(b, "GlowArt") }
func BenchmarkCurrentSong(b *testing.B)  { benchHelper(b, "CurrentSong") }

// memoCases are the view's memoized values; miss is the allocations a remake
// may cost.
var memoCases = []struct {
	name string
	miss float64
	get  func(a *app)
}{
	{"LikedCount", 0, func(a *app) { sinkInt = a.likedCount() }},
	// LikedCollection, LikedLine, PlaylistCards and CachedCollection say
	// counts in plural messages: see TotalLength. 25 allocs today: 2000
	// songs, and the line under the title.
	{"LikedCollection", 29, func(a *app) { sinkCollection = a.likedCollection() }},
	{"SidebarItems", 26, func(a *app) { sinkSideItems = a.sidebarItems() }}, // 22 allocs today.
	// 134 allocs today: a card's line for each of 90 items.
	{"HomeCards", 155, func(a *app) { sinkCards = a.homeCards() }},
	{"ReleaseCards", 25, func(a *app) { sinkCardRow = a.releaseCards() }},    // 21 allocs today.
	{"MoodKeys", 15, func(a *app) { sinkKeys = a.moodKeys() }},               // 13 allocs today.
	{"LikedLine", 19, func(a *app) { sinkString = a.likedLine() }},           // 16 allocs today.
	{"PlaylistCards", 122, func(a *app) { sinkCardRow = a.playlistCards() }}, // 106 allocs today.
	{"AlbumCards", 36, func(a *app) { sinkCardRow = a.albumCards() }},        // 31 allocs today.
	{"ArtistCards", 13, func(a *app) { sinkCardRow = a.artistCards() }},      // 11 allocs today.
	{"TypedText", 2, func(a *app) { sinkTyped = a.typedText("lumen") }},
	{"FoundText", 37, func(a *app) { sinkFound = a.foundText("lumen", a.cat.Albums) }}, // 32 allocs today.
	// A scan of 2000 songs, without allocating.
	{"HoldsCurrent", 0, func(a *app) { sinkBool = a.holdsCurrent(a.cat.Songs) }},
	{"RowText", 7, func(a *app) { sinkRowText = a.rowText(&a.cat.Songs[0]) }}, // 6 allocs today, the map included.
	// 35 allocs today: the tracks of a scan of 2000 songs, and the line.
	{"CachedCollection/album", 41, func(a *app) {
		sinkCollection = a.cachedCollection(fetchstate.Resource{Kind: fetchstate.Album, ID: "neon-tide"})
	}},
	{"CachedCollection/playlist", 25, func(a *app) { // 21 allocs today.
		sinkCollection = a.cachedCollection(fetchstate.Resource{Kind: fetchstate.Playlist, ID: "late-night-drive"})
	}},
}

// BenchmarkMemo measures each memoized value kept (hit) and remade (miss),
// as after the catalog, the likes or a feed changed.
func BenchmarkMemo(b *testing.B) {
	a := helperApp()
	for _, tc := range memoCases {
		b.Run(tc.name+"/hit", func(b *testing.B) {
			tc.get(a)
			b.ReportAllocs()
			for b.Loop() {
				tc.get(a)
			}
		})
		b.Run(tc.name+"/miss", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				a.memo = viewMemo{}
				tc.get(a)
			}
		})
	}
}
