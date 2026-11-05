package main

import (
	"yt-music-player/internal/catalog"
	"yt-music-player/locales"

	"github.com/egoist/mygo/ui"
)

func (a *app) sidebar(c *ui.Context) {
	ui.Column(c).Width(th.Layout.Sidebar).Shrink(0).Children(func() {
		top := th.Space(3)
		if titleBarRoom(c, th.Layout.SidebarSide, 0) > 0 {
			// The window controls sit over the sidebar.
			top = c.TitleBar().Height - th.Layout.Gutter
		}
		ui.Box(c).Height(top).Shrink(0).DragWindow()
		brand(c)
		ui.Column(c).Padding(th.Space(4.5), th.Space(3), 0).Gap(th.Space(0.5)).FocusGroup(ui.Vertical).Children(func() {
			for i := range navItems {
				a.navItem(c, &navItems[i])
			}
		})
		ui.Box(c).Height(1).Background(th.Color.Line).Margin(th.Space(4.5), th.Space(6))
		ui.Row(c).Padding(0, th.Space(4), th.Space(2), th.Space(6)).AlignItems(ui.Center).Children(func() {
			eyebrow(c, locales.SidebarPlaylists()).Grow(1)
			iconButton(c, "new-playlist", icPlus, locales.NewPlaylist(), btnSmall, iconOpts{})
		})
		playlists := ui.Scroll(c.Key("playlists")).Grow(1).Padding(0, th.Space(3), th.Space(4)).Gap(th.Space(0.5)).
			FocusGroup(ui.Vertical)
		playlists.Children(func() {
			a.playlistItem(c, a.likedItem())
			items := a.sidebarItems()
			for i := range items {
				a.playlistItem(c, &items[i])
			}
		})
	})
}

func brand(c *ui.Context) {
	ui.Row(c).Padding(0, th.Space(6)).Gap(th.Space(2.5)).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(26, 26).Shrink(0).Draw(paintMark)
		ui.Text(c, "YT Music Player").FontSize(th.Type.Brand).FontWeight(600).LetterSpacing(-0.3)
	})
}

// paintMark draws the brand mark (docs/brand.md#mark) in r: five queue rows
// whose lengths draw a play triangle, the middle one, playing, in the accent.
func paintMark(p *ui.Painter, r ui.Rect) {
	// The mark's 120-unit box: rows 14 thick, 22 apart, from x 23 to 23+74·(1-|k|/2.5).
	s := r.W / 120
	w := 14 * s
	for k := -2; k <= 2; k++ {
		c := th.Color.Text
		if k == 0 {
			c = th.Color.Accent
		}
		length := 74 * (1 - float32(max(k, -k))/2.5) * s
		y := r.Y + (60+22*float32(k))*s
		p.Fill(ui.Rect{X: r.X + 23*s - w/2, Y: y - w/2, W: length + w, H: w}, c, w/2)
	}
}

// navTarget is a sidebar item or an Explore tile. key is the English
// label, boxed once; label says it in the app's language.
type navTarget struct {
	key   any
	path  string
	label func() string
	icon  *ui.SVG
}

var navItems = [...]navTarget{
	{key: "Home", path: pathHome, label: locales.NavHome, icon: icHome},
	{key: "Explore", path: pathExplore, label: locales.NavExplore, icon: icCompass},
	{key: "Library", path: pathLibrary, label: locales.NavLibrary, icon: icLibrary},
}

func (a *app) navItem(c *ui.Context, item *navTarget) {
	path, icon, label := item.path, item.icon, item.label()
	// Playlists live in the library, so it stays lit while one is open.
	isPlaylistOpen := path == pathLibrary && (a.onPath(playlistPrefix) || a.onPath(pathLiked))
	isActive := a.onPath(path) || isPlaylistOpen
	// KeepFocus: Space must then play, not open the page again.
	b := ui.ButtonBase(c.Key(item.key)).Height(42).Padding(0, th.Space(3.5)).Gap(th.Space(3.5)).
		Radius(th.Radius.Control).AlignItems(ui.Center).Justify(ui.Start).Cursor(ui.CursorPointer).KeepFocus().
		Selected(isActive)
	iconCol, textCol := th.Color.Muted, th.Color.Muted
	switch {
	case isActive:
		b.Background(th.Color.Raised)
		iconCol, textCol = th.Color.Accent, th.Color.Text
	case b.Hovered():
		b.Background(th.Color.NavHover)
		iconCol, textCol = th.Color.Text, th.Color.Text
	}
	b.Transition(colorFade)
	b.Children(func() {
		ui.Icon(c, icon).Size(20, 20).TextColor(iconCol)
		ui.Text(c, label).FontSize(th.Type.Title).FontWeight(600).TextColor(textCol).SingleLine()
	})
	if b.Clicked() {
		a.router.Push(path)
	}
}

type sideItem struct {
	// key is path, boxed once.
	key             any
	path, name, sub string
	art             catalog.Art
}

// likedItem is Liked music's, in the app's language.
func (a *app) likedItem() *sideItem {
	m := &a.memo.likedItem
	if m.key == nil {
		*m = sideItem{key: pathLiked, path: pathLiked, name: locales.LikedMusic(), sub: locales.AutoPlaylist(), art: artLiked}
	}
	return m
}

func (a *app) sidebarItems() []sideItem {
	m := &a.memo.sidebar
	if m.stale(a.cat.Gen()) {
		playlists := a.cat.LibraryPlaylists()
		m.v = make([]sideItem, len(playlists))
		for i, pl := range playlists {
			path := playlistPath(pl.ID)
			m.v[i] = sideItem{key: path, path: path, name: pl.Name, sub: playlistLine(pl, false), art: pl.Art}
		}
	}
	return m.v
}

func (a *app) playlistItem(c *ui.Context, item *sideItem) {
	isActive := a.onPath(item.path)
	b := ui.ButtonBase(c.Key(item.key)).Height(56).Padding(0, th.Space(2.5)).Gap(th.Space(3)).Radius(th.Radius.Control).
		AlignItems(ui.Center).Justify(ui.Start).Cursor(ui.CursorPointer).KeepFocus().Label(item.name).Selected(isActive)
	switch {
	case isActive:
		// Selected tints the text for an accent fill this item doesn't use.
		b.Background(th.Color.Raised).TextColor(th.Color.Text)
	case b.Hovered():
		b.Background(th.Color.NavHover)
	}
	b.Children(func() {
		artBox := coverArt(c, item.art, 40, th.Radius.Cover)
		if item.art == artLiked {
			artBox.Center().Children(func() { ui.Icon(c, icHeartFilled).Size(16, 16).TextColor(th.Color.Light) })
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(th.Space(0.5)).Children(func() {
			ui.Text(c, item.name).FontSize(th.Type.Label).FontWeight(600).SingleLine()
			ui.Text(c, item.sub).FontSize(th.Type.Small).TextColor(th.Color.Muted).SingleLine()
		})
	})
	if b.Clicked() {
		a.router.Push(item.path)
	}
}
