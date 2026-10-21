package main

import (
	"strconv"
	"time"
	"yt-music-player/internal/artwork"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/duration"
	"yt-music-player/internal/fetchstate"
	"yt-music-player/internal/loader"
	"yt-music-player/internal/notice"
	"yt-music-player/internal/quiet"
	"yt-music-player/internal/remote"
	"yt-music-player/internal/session"
	"yt-music-player/internal/settings"
	"yt-music-player/internal/theme"
	"yt-music-player/locales"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

const (
	tabQueue = iota
	tabLyrics
)

// app is the UI state.
type app struct {
	// cat is UI-thread only.
	cat *catalog.Catalog
	// client is ytmd: the loader's and session's Source, the Controller
	// of playback, and the Mirror of what plays. sup keeps it connected;
	// nil until connect.
	client   *remote.Client
	mirror   *remote.Mirror
	sup      *remote.Supervisor
	daemon   daemonState
	progress progress
	bar      barText
	router   *ui.Router

	updates poster
	// post is made once: a method value allocates.
	post    func(fn func())
	loader  *loader.Loader
	fetches fetchstate.Registry
	// art is nil until the first build.
	art *artwork.Loader

	search searchState
	panel  panelState
	page   pageState
	seen   seenState

	// goTo, showLook and actions are made once: a func made per build
	// allocates.
	goTo     [3]func()
	showLook func()
	actions  actions

	session *session.Session
	sheet   sheetState
	// notices wait for a build to be told: the daemon's, the failed
	// controls' and switches', the settings' and the themes'.
	notices []notice.Notice
	startup startupState
	memo    viewMemo

	theme, contrast theme.Theme
	look            lookState

	quiet quiet.GC

	paints paints
}

// paints are painters made once, reading what the last build set: a
// closure per build would allocate.
type paints struct {
	glow   ui.Color
	hero   cover
	glowFn func(*ui.Painter, ui.Rect)
	heroFn func(*ui.Painter, ui.Rect)
}

type actions struct {
	togglePlay, next, prev, focusSearch, search func()
	// chooseChip is by choice, then option.
	chooseChip [len(layoutChoices)][]func()
}

// searchState's loc is the router location query last mirrored.
type searchState struct {
	query, loc string
	field      ui.Handle
}

type panelState struct {
	isShown bool
	tab     int
}

// pageState is what pages keep while another page shows.
type pageState struct {
	mood    int
	library [len(libraryTabs)]ui.ListState
}

// seenState is what reconcile last acted on (the loader's location, the
// announced song, the language), so it acts once per change. hasSong
// tells that song is the daemon's: the song current as the app connects
// is not announced.
type seenState struct {
	loc, song, lang string
	hasSong         bool
	health          remote.Health
}

// newApp starts with an empty catalog; ytmd has the queue.
func newApp(post func(func())) *app {
	return appWith(catalog.New(nil, nil, nil), post)
}

// newDemoApp starts with the placeholder's music, which only a
// placeholder ytmd serves. Apart from newApp so that the release build,
// which calls it only behind the constant demo, links no placeholder
// catalog.
func newDemoApp(post func(func())) *app {
	cat := catalog.Placeholder()
	// The library and likes come from the session.
	cat.SetLibrary(nil)
	return appWith(cat, post)
}

// appWith makes the app over cat. clientPost runs the remote Client's
// funcs on the UI thread; nil is a.post, which tests replace to run them
// apart from the app's own.
func appWith(cat *catalog.Catalog, clientPost func(func())) *app {
	r := ui.NewRouter(pathHome)
	// Pages draw over the panel's glow; a slide would paint an opaque
	// backdrop over it while they move.
	r.Transition = ui.TransitionFade
	a := &app{
		cat:    cat,
		router: r,
		panel:  panelState{isShown: true},
	}
	a.post = a.updates.post
	if clientPost == nil {
		clientPost = a.post
	}
	a.client = remote.NewClient(clientPost, a.daemonHandler())
	a.mirror = a.client.Mirror()
	a.loader = loader.New(a.client, &a.fetches, cat, a.post)
	a.connectAccount(cat, clientPost)
	for i, path := range [...]string{pathHome, pathExplore, pathLibrary} {
		a.goTo[i] = func() { a.router.Push(path) }
	}
	a.showLook = a.openLook
	ctl := a.client
	a.actions = actions{
		togglePlay: ctl.Toggle, next: ctl.Next, prev: ctl.Prev,
		// The app has one window, which Focus finds without a Context.
		focusSearch: func() { a.search.field.Focus() },
		search:      func() { a.searchFor(a.search.query) },
	}
	for choice, lc := range layoutChoices {
		chooses := make([]func(), len(lc.chips))
		for option := range lc.chips {
			chooses[option] = func() { a.chooseChip(choice, option) }
		}
		a.actions.chooseChip[choice] = chooses
	}
	// An empty Dir saves nothing and never fails, until useSettings.
	a.look.saver = settings.NewSaver("", nil)
	a.setTheme(theme.Default())
	return a
}

func (a *app) view(c *ui.Context) {
	a.quiet.Note()
	a.applyTheme(c)
	a.useArt()
	c.Root().Background(th.Color.Base)
	a.shortcuts(c)
	// Before reconcile adds notices: a toast added in a pass shows in the
	// next one, consistently.
	toasts(c)
	a.reconcile(c)
	layout := &th.Layout
	ui.Column(c).Fill().Children(func() {
		if layout.PlayerBar == theme.Top {
			a.playerBar(c)
		}
		a.body(c)
		if layout.PlayerBar == theme.Bottom {
			a.playerBar(c)
		}
	})
	if layout.PlayerBar == theme.Bottom {
		// The top gutter sits above every panel, so it needs its own drag
		// area; a player bar on top drags the window itself.
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Height(layout.Gutter).DragWindow()
	}
	a.banner(c)
	a.signInSheet(c)
	a.appearanceSheet(c)
}

// reconcile catches up with changes since the last pass, before anything
// builds. Its steps are independent of order.
func (a *app) reconcile(c *ui.Context) {
	a.syncLanguage()
	a.syncQuery()
	a.syncLoad()
	a.syncAccount(c)
	a.syncNotices(c)
	a.announceLink(c)
	a.syncPlayback(c)
	a.announceSong(c)
}

func (a *app) shortcuts(c *ui.Context) {
	c.OnShortcut(0, ui.KeySpace, a.actions.togglePlay)
	c.OnShortcut(ui.Cmd, ui.KeyRight, a.actions.next)
	c.OnShortcut(ui.Cmd, ui.KeyLeft, a.actions.prev)
	c.OnShortcut(ui.Cmd, ui.KeyK, a.actions.focusSearch)
	// So the sidebar's pages stay reachable in layouts that hide it.
	for i, fn := range a.goTo {
		c.OnShortcut(ui.Cmd, ui.Key1+ui.Key(i), fn)
	}
	c.OnShortcut(ui.Cmd, ui.KeyComma, a.showLook)
}

// announceSong tells screen readers of a new current song: a shortcut or
// a song ending changes it without moving focus.
func (a *app) announceSong(c *ui.Context) {
	if a.mirror.StateGen() == 0 {
		return
	}
	id := a.currentID()
	if id == a.seen.song && a.seen.hasSong {
		return
	}
	isFirst := !a.seen.hasSong
	a.seen.song, a.seen.hasSong = id, true
	if s := a.currentSong(); !isFirst && s.ID != "" {
		c.Announce(locales.NowPlayingSong(locales.SongByArtist(s.Title, s.Artist)))
	}
}

func (a *app) body(c *ui.Context) {
	layout := &th.Layout
	left, right := gutters()
	top, bottom := layout.Gutter, float32(0)
	if layout.PlayerBar == theme.Top {
		top, bottom = 0, layout.Gutter
	}
	body := ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Padding(top, right, bottom, left)
	panelOpen := a.panel.isShown && layout.PanelSide != theme.Hidden
	shown := body.Animate("panel", boolToFloat(panelOpen), 260*time.Millisecond)
	body.Children(func() {
		if layout.SidebarSide == theme.Left {
			a.sidebar(c)
		}
		if layout.PanelSide == theme.Left {
			a.panelSlot(c, shown)
		}
		a.mainPanel(c, shown)
		if layout.PanelSide == theme.Right {
			a.panelSlot(c, shown)
		}
		if layout.SidebarSide == theme.Right {
			a.sidebar(c)
		}
	})
}

// gutters is the body's padding: the sidebar has no fill and reaches the
// window's edge, while panels keep a gutter.
func gutters() (left, right float32) {
	layout := &th.Layout
	left, right = layout.Gutter, layout.Gutter
	switch layout.SidebarSide {
	case theme.Left:
		left = 0
	case theme.Right:
		right = 0
	}
	return left, right
}

// panelSlot slides the now-playing panel in from the window's edge as
// shown goes 0 to 1; the gutter to the main panel shows last.
func (a *app) panelSlot(c *ui.Context, shown float32) {
	if shown <= 0 {
		return
	}
	layout := &th.Layout
	width := (layout.Panel + layout.Gutter) * shown
	slot := ui.Row(c).Width(width).Shrink(0).AlignItems(ui.Stretch).ClipX()
	if layout.PanelSide == theme.Left {
		// A row overflows only past its end, so a left panel needs a
		// negative margin to slide out past the window's edge.
		slot.Children(func() { a.nowPlaying(c, min(0, width-layout.Panel)) })
		return
	}
	slot.Justify(ui.End).Children(func() { a.nowPlaying(c, 0) })
}

// titleBarRoom is the width the window controls take from the top of a
// region at side, less the covered fraction (0 to 1) of a region sliding
// over it. With the player bar on top, the controls sit over the bar.
func titleBarRoom(c *ui.Context, side theme.Side, covered float32) float32 {
	if th.Layout.PlayerBar == theme.Top {
		return 0
	}
	bar := c.TitleBar()
	room := bar.Right
	if side == theme.Left {
		room = bar.Left
	}
	return room * (1 - covered)
}

func contentWidth(c *ui.Context, panelShown float32) float32 {
	windowWidth, _ := c.Size()
	layout := &th.Layout
	var sidebar float32
	if layout.SidebarSide != theme.Hidden {
		sidebar = layout.Sidebar
	}
	left, right := gutters()
	return windowWidth - sidebar - left - right - (layout.Panel+layout.Gutter)*panelShown - 2*layout.Page
}

func (a *app) mainPanel(c *ui.Context, panelShown float32) {
	// Not clipped: a rounded clip around anything transformed (a pressed
	// pill) draws through a panel-sized mask texture. Pages scroll in
	// square clips, and panelCorners rounds them.
	panel := ui.Column(c).Grow(1).MinWidth(0).Radius(th.Layout.PanelRadius).Background(th.Color.Panel)
	pt := &a.paints
	pt.glow = panel.AnimateColor("glow", coverOf(a.glowArt()).from, ui.Smooth)
	if pt.glowFn == nil {
		pt.glowFn = func(p *ui.Painter, r ui.Rect) {
			p.FillGradient(
				ui.Rect{X: r.X, Y: r.Y, W: r.W, H: 380},
				ui.LinearGradient{From: pt.glow.Alpha(0.3), To: pt.glow.Alpha(0), Angle: 180, Oklab: true},
				th.Layout.PanelRadius,
			)
		}
	}
	panel.Draw(pt.glowFn)
	width := contentWidth(c, panelShown)
	panel.Children(func() {
		a.topBar(c, width, panelShown)
		a.pages(c, width)
		panelCorners(c)
	})
}

// panelCorners paints the window background over the main panel's bottom
// corners, outside its rounding, hiding what scrolls there (the top bar
// does not scroll). In every layout the window background lies beside
// them: the sidebar and player bar have no fill, panels keep a gutter.
func panelCorners(c *ui.Context) {
	radius := th.Layout.PanelRadius
	ui.Box(c).Absolute().Bottom(0).Size(radius, radius).Left(0).Draw(paintCorner[0])
	ui.Box(c).Absolute().Bottom(0).Size(radius, radius).Right(0).Draw(paintCorner[1])
}

var paintCorner = [2]func(*ui.Painter, ui.Rect){cornerPainter(false), cornerPainter(true)}

func cornerPainter(right bool) func(*ui.Painter, ui.Rect) {
	return func(p *ui.Painter, r ui.Rect) {
		// Stroke the outline, grown by radius, of a larger panel with the
		// same corner at r: clipped to r, the stroke covers just what lies
		// outside the rounding.
		radius := th.Layout.PanelRadius
		size := 8 * radius
		x := r.X - radius
		if right {
			x = r.X - size
		}
		outline := ui.Rect{X: x, Y: r.Y - size, W: size + 2*radius, H: size + 2*radius}
		p.Clip(r, 0, func() { p.Stroke(outline, th.Color.Base, 2*radius, radius) })
	}
}

// clocks caches clock's results by second: every row shows its song's
// length in every build.
var clocks [60 * 60]string

// clock says seconds as the TUI and ytm do (duration.Clock).
func clock(seconds int) string {
	if seconds < 0 || seconds >= len(clocks) {
		return duration.Clock(float64(seconds))
	}
	if clocks[seconds] == "" {
		clocks[seconds] = duration.Clock(float64(seconds))
	}
	return clocks[seconds]
}

// numerals caches numeral's results: table rows show their position in
// every build, and strconv allocates past 99. Playlists hold up to 5000
// songs.
var numerals [5001]string

func numeral(n int) string {
	if n < 0 || n >= len(numerals) {
		return strconv.Itoa(n)
	}
	if numerals[n] == "" {
		numerals[n] = strconv.Itoa(n)
	}
	return numerals[n]
}

// totalLength starts with "· ": the meta line shows it after the owner.
func totalLength(songs []catalog.Song) string {
	return locales.CollectionLength(len(songs), duration.Minutes(catalog.TotalSeconds(songs)))
}

// syncLanguage drops the text made in the language shown before, so the
// view says the new one: the memo, the player bar's strings and the
// themes' names.
func (a *app) syncLanguage() {
	lang := i18n.Locale()
	if lang == a.seen.lang {
		return
	}
	isFirst := a.seen.lang == ""
	a.seen.lang = lang
	if isFirst {
		return
	}
	a.memo = viewMemo{}
	a.bar.seek, a.bar.from = kept[[2]int, string]{}, kept[string, string]{}
	if a.look.isOpen {
		a.themeOptions()
	}
}
