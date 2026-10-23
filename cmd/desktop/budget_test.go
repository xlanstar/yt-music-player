package main

import (
	"bytes"
	"context"
	"slices"
	"testing"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/budget"
	"yt-music-player/internal/catalog"
)

// frameBudget is what a steady frame of a page may cost; builds is 1 for a
// page that does not animate. liveKB bounds the heap the screen retains:
// the Tester and the app, with the catalog and decoded thumbnails.
type frameBudget struct {
	allocs, bytes, builds float64
	liveKB                int64
}

// frameBudgets are BenchmarkPages' ceilings. MyGo 0.6's element state keeps
// each element's last layout, for its layout cache: about 15% more live KB.
var frameBudgets = map[string]frameBudget{
	"Home":              {30, 1160, 1, 3730}, // 26 allocs, 1008 B, 3236 KB today.
	"Explore":           {11, 540, 1, 3260},  // 9 allocs, 464 B, 2829 KB today.
	"Library/Playlists": {27, 1500, 1, 2430}, // 23 allocs, 1296 B, 2113 KB today.
	"Library/Songs":     {33, 2030, 1, 3210}, // 28 allocs, 1760 B, 2788 KB today.
	"Library/Albums":    {29, 1900, 1, 2500}, // 25 allocs, 1648 B, 2171 KB today.
	"Library/Artists":   {29, 1880, 1, 2490}, // 25 allocs, 1632 B, 2163 KB today.
	"Album":             {21, 1150, 1, 2140}, // 18 allocs, 992 B, 1858 KB today.
	"Playlist":          {21, 1150, 1, 2620}, // 18 allocs, 992 B, 2277 KB today.
	"LikedMusic":        {20, 1070, 1, 2470}, // 17 allocs, 928 B, 2144 KB today.
	"Search":            {15, 1180, 1, 2220}, // 13 allocs, 1024 B, 1926 KB today.
	"Home/PanelHidden":  {25, 980, 1, 3520},  // 21 allocs, 848 B, 3059 KB today.
	// The equalizer only paints: the frame builds once.
	"Home/Playing":       {30, 1160, 1, 3730}, // 26 allocs, 1008 B, 3237 KB today.
	"Library/Songs/2000": {33, 2030, 1, 4570}, // 28 allocs, 1760 B (as 18 songs), 3969 KB today.
	// The covers' pixels at the size they show.
	"Library/Albums/Thumbnails": {29, 1900, 1, 8680}, // 25 allocs, 1648 B, 7547 KB today.
	// Some 90 covers of 352 px.
	"Home/Feed":     {30, 1160, 1, 54070}, // 26 allocs, 1008 B, 47009 KB today.
	"Playlist/2000": {21, 1150, 1, 4800},  // 18 allocs, 992 B, 4166 KB today.
	// The sheet's radios and segmented controls, MyGo's, built only
	// while it is open.
	"Home/Appearance": {69, 2230, 1, 4270}, // 61 allocs, 1952 B, 3710 KB today, with the background player's switches.
	// The sheet cleans the pasted header once per edit, not per build.
	"Home/SignIn":     {36, 1350, 1, 4150}, // 31 allocs, 1168 B, 3604 KB today.
	"LikedMusic/2000": {20, 1070, 1, 5200}, // 17 allocs, 928 B, 4515 KB today.
}

// frameRuns is how many steady frames a budget averages; they cost the same.
const frameRuns = 10

func TestBudgetFrames(t *testing.T) {
	if budget.Race {
		t.Skip("the race detector allocates on its own")
	}
	for _, pc := range pageCases {
		t.Run(pc.name, func(t *testing.T) {
			want, ok := frameBudgets[pc.name]
			if !ok {
				t.Fatal("no budget: add the page to frameBudgets")
			}
			s := newScreen(t, pc.setup)
			s.steady()
			s.builds = 0
			got := budget.Measure(frameRuns, s.tt.Frame)
			builds := float64(s.builds) / (frameRuns + 1)
			live := retainedKB(t, &s)
			t.Logf("a frame: %.0f allocs, %.0f B, %.2f builds; %d KB live", got.Allocs, got.Bytes, builds, live)
			overBudget(t, "allocs a frame", got.Allocs, want.allocs)
			overBudget(t, "bytes a frame", got.Bytes, want.bytes)
			overBudget(t, "builds a frame", builds, want.builds)
			overBudget(t, "KB live", float64(live), float64(want.liveKB))
		})
	}
}

func overBudget(t *testing.T, what string, got, ceiling float64) {
	t.Helper()
	if got > ceiling {
		t.Errorf("%.2f %s, budget %.2f", got, what, ceiling)
	}
}

// interactionBudget is what an op may cost, input plus settled frames,
// averaged over runs ops after a first one; with perBuild, allocs and bytes
// are per build instead.
type interactionBudget struct {
	allocs, bytes, builds float64
	runs                  int
	perBuild              bool
}

// interactionBudgets are BenchmarkInteractions' ceilings; builds are exact:
// the Tester's clock stands still, so a hover settles its 20 frames, each
// built, at the input's instant. The hero's slow posed thumbnails are held
// per build; hovers, up to 400 ms an op, take fewer runs.
var interactionBudgets = map[string]interactionBudget{
	"HoverRows":  {35, 6120, 1, 20, false},   // 30 allocs, 5319 B today: the row's fade only paints.
	"HoverCards": {598, 23240, 20, 3, false}, // 520 allocs, 20203 B today.
	"HoverPlay":  {664, 48380, 20, 3, false}, // 560 allocs, 27621 B today.
	"HoverHero":  {31, 1180, 20, 3, true},    // 26.1 allocs, 1012 B a build today.
	// The placeholder's painted sleeves.
	"HoverHero/Painted": {601, 23290, 20, 3, false}, // 522 allocs, 20245 B today.
	// Rows coming into view, their text laid out anew, then the row the
	// scroll brought under the pointer built hovered (MyGo 0.6).
	"ScrollSongs": {518, 40650, 2, 10, false}, // 450 allocs, 35344 B today.
	// 3 builds deleting a letter, 2 typing one: 53 over 21 ops.
	"TypeSearch":  {114, 8130, 2.53, 20, false}, // 99 allocs, 7065 B today.
	"LibraryTabs": {111, 118140, 1, 8, false},   // 96 allocs, 102725 B today, with MyGo 0.6's layout cache.
	// With the other front-end's request and ytmd's work, in this process.
	"remote-event": {79, 6040, 1, 20, false}, // 68 allocs, 5248 B today.
}

func TestBudgetInteractions(t *testing.T) {
	if budget.Race {
		t.Skip("the race detector allocates on its own")
	}
	for _, ic := range interactionCases {
		t.Run(ic.name, func(t *testing.T) {
			want, ok := interactionBudgets[ic.name]
			if !ok {
				t.Fatal("no budget: add the interaction to interactionBudgets")
			}
			s, op := interacting(t, ic.setup, ic.start)
			s.builds = 0
			i := 0
			got := budget.Measure(want.runs, func() {
				op(i)
				i++
			})
			builds := float64(s.builds) / float64(want.runs+1)
			per := "an op"
			if want.perBuild {
				per = "a build"
				got.Allocs, got.Bytes = got.Allocs/builds, got.Bytes/builds
			}
			t.Logf("%s: %.1f allocs, %.0f B; %.2f builds an op", per, got.Allocs, got.Bytes, builds)
			overBudget(t, "allocs "+per, got.Allocs, want.allocs)
			overBudget(t, "bytes "+per, got.Bytes, want.bytes)
			overBudget(t, "builds an op", builds, want.builds)
		})
	}
}

// thumbSource is the placeholder with thumbnailServer art, as YouTube
// Music's, except the playing song (kept): its cover tints the window,
// which would ease to each color as thumbnails load, 20 Tester frames each.
type thumbSource struct {
	*catalog.PlaceholderSource
	kept string
}

func (s thumbSource) song(song *catalog.Song) {
	if song.ID != s.kept {
		song.Art = thumbArt(song.Art)
	}
}

func (s thumbSource) songs(songs []catalog.Song) []catalog.Song {
	for i := range songs {
		s.song(&songs[i])
	}
	return songs
}

func thumbAlbums(albums []catalog.Album) []catalog.Album {
	for i := range albums {
		albums[i].Art = thumbArt(albums[i].Art)
	}
	return albums
}

func (s thumbSource) Search(ctx context.Context, q string) (catalog.Results, error) {
	r, err := s.PlaceholderSource.Search(ctx, q)
	r.Songs, r.Albums = s.songs(r.Songs), thumbAlbums(r.Albums)
	return r, err
}

func (s thumbSource) Home(ctx context.Context) (catalog.Home, error) {
	h, err := s.PlaceholderSource.Home(ctx)
	for _, sh := range h.Shelves {
		for i := range sh.Items {
			it := &sh.Items[i]
			s.song(&it.Song)
			it.Album.Art = thumbArt(it.Album.Art)
			it.Playlist.Art = thumbArt(it.Playlist.Art)
			it.Artist.Art = thumbArt(it.Artist.Art)
		}
	}
	return h, err
}

func (s thumbSource) Explore(ctx context.Context) (catalog.Explore, error) {
	e, err := s.PlaceholderSource.Explore(ctx)
	e.NewReleases, e.Trending = thumbAlbums(e.NewReleases), s.songs(e.Trending)
	return e, err
}

func (s thumbSource) Library(ctx context.Context) (catalog.Library, error) {
	l, err := s.PlaceholderSource.Library(ctx)
	l.Songs, l.Albums = s.songs(l.Songs), thumbAlbums(l.Albums)
	for i := range l.Playlists {
		l.Playlists[i].Art = thumbArt(l.Playlists[i].Art)
	}
	for i := range l.Artists {
		l.Artists[i].Art = thumbArt(l.Artists[i].Art)
	}
	return l, err
}

// thumbScreen shows loc over thumbSource once its thumbnails loaded.
func thumbScreen(loc string) func(testing.TB) *screen {
	return func(tb testing.TB) *screen {
		// The song ytmd's queue starts at.
		playing := catalog.Placeholder().Songs[0].ID
		a, q := sourceApp(tb, thumbSource{catalog.NewPlaceholderSource(0), playing})
		a.loader.Debounce = 0
		a.router.Push(loc)
		return showApp(tb, a, q)
	}
}

func playingScreen(playing, focused bool) func(testing.TB) *screen {
	return func(tb testing.TB) *screen {
		a, q, e := playbackApp(tb, &instantResolver{})
		startPlaying(tb, a, q, e)
		a.client.Seek(60)
		a.progress.setFocused(focused)
		if !playing {
			a.client.Pause()
		}
		syncDaemon(tb, q)
		return showApp(tb, a, q)
	}
}

// Idle screens must ask for no frame for longer than any step on them: the
// position's 1 s step while playing, else a few display frames.
const (
	playingQuiet = time.Second + 100*time.Millisecond
	pageQuiet    = 250 * time.Millisecond
)

// idleCases must draw no frame for quiet once settled: an idle window
// draws nothing and frees GPU memory. Playing focused is the control (the
// equalizer moves).
var idleCases = []struct {
	name   string
	screen func(testing.TB) *screen
	quiet  time.Duration
	moves  bool
}{
	{"Paused", playingScreen(false, true), playingQuiet, false},
	{"Playing/focused", playingScreen(true, true), playingQuiet, true},
	{"Home", thumbScreen(pathHome), pageQuiet, false},
	{"Explore", thumbScreen(pathExplore), pageQuiet, false},
	{"Library", thumbScreen(libraryPath(tabAlbums)), pageQuiet, false},
	{"Search", thumbScreen(searchPath("lumen")), pageQuiet, false},
	// The next frame is due as the position shown turns 65 s.
	{"Playing/unfocused", playingScreen(true, false), playingQuiet, false},
}

func TestBudgetIdleFrames(t *testing.T) {
	for _, tc := range idleCases {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.screen(t)
			s.steady()
			// The equalizer only repaints (Painter.After), which builds
			// nothing: its frames show in the pixels.
			before := slices.Clone(s.tt.Image().Pix)
			s.builds = 0
			s.tt.Advance(tc.quiet)
			moved := s.builds > 0 || s.tt.FramePending() || !bytes.Equal(before, s.tt.Image().Pix)
			switch {
			case moved && !tc.moves:
				t.Errorf("drew while idle: %d builds, frame pending %v", s.builds, s.tt.FramePending())
			case !moved && tc.moves:
				t.Error("drew nothing while the equalizer moves")
			}
		})
	}
}

// framesPerMinute counts the frames a minute of s playing from pos asks
// for, each drawn when the previous one asked.
func framesPerMinute(p *progress, s *api.State, pos float64) int {
	asked := 0
	for end := pos + 60; pos < end; asked++ {
		next := p.nextFrame(s, pos)
		if next <= 0 {
			break
		}
		pos += next.Seconds()
	}
	return asked
}

func TestBudgetPlayingFrames(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		playing, focused, visible bool
		want                      int
	}{
		{"focused", true, true, true, 60},
		{"unfocused", true, false, true, 12},
		{"hidden", true, true, false, 0},
		{"paused", false, true, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := api.State{Playing: tc.playing, Current: "s", Duration: 200}
			var p progress
			p.setFocused(tc.focused)
			p.setShown(tc.visible)
			if got := framesPerMinute(&p, &s, 30); got != tc.want {
				t.Errorf("%d frames a minute, want %d", got, tc.want)
			}
		})
	}
}

func TestBudgetHelpers(t *testing.T) {
	a := helperApp(t)
	for _, tc := range helperCases {
		t.Run(tc.name, func(t *testing.T) {
			i := 0
			budget.Allocs(t, tc.limit, 100, func() {
				tc.call(a, i)
				i++
			})
		})
	}
}

func TestBudgetMemo(t *testing.T) {
	a := helperApp(t)
	for _, tc := range memoCases {
		t.Run(tc.name, func(t *testing.T) {
			budget.Allocs(t, 0, 100, func() { tc.get(a) })
			budget.Allocs(t, tc.miss, 20, func() {
				a.memo = viewMemo{}
				tc.get(a)
			})
		})
	}
}
