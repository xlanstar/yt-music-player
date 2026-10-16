package main

import (
	"strconv"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/fetchstate"
	"yt-music-player/internal/session"
	"yt-music-player/locales"

	"github.com/egoist/mygo/ui"
)

type libraryTab uint8

const (
	tabPlaylists libraryTab = iota
	tabSongs
	tabAlbums
	tabArtists
)

var libraryTabs = [...]struct {
	name func() string
	slug string
}{
	tabPlaylists: {locales.SidebarPlaylists, ""},
	tabSongs:     {locales.Songs, "songs"},
	tabAlbums:    {locales.Albums, "albums"},
	tabArtists:   {locales.Artists, "artists"},
}

func libraryTabOf(slug string) libraryTab {
	for i, t := range libraryTabs {
		if t.slug == slug {
			return libraryTab(i)
		}
	}
	return tabPlaylists
}

// libraryPath keeps the tab in the query: one page keeps its state, while
// each tab gets a history entry so Back returns to the previous tab.
func libraryPath(tab libraryTab) string {
	if slug := libraryTabs[tab].slug; slug != "" {
		return pathLibrary + "?tab=" + slug
	}
	return pathLibrary
}

func (a *app) libraryHeader(c *ui.Context, tab libraryTab) {
	ui.Column(c).Padding(0, rowBleed).Gap(th.Space(6)).Margin(0, 0, th.Space(6), 0).Children(func() {
		ui.Row(c).Gap(th.Space(4)).AlignItems(ui.Center).Children(func() {
			heading(c, locales.NavLibrary(), th.Type.Page, 1).Grow(1)
			pill(c, "sort", nil, locales.RecentlyAdded(), pillGhost).Height(34).Padding(0, th.Space(3.5))
		})
		// The route owns the tab, so choosing one navigates.
		chosen := int(tab)
		tabs := ui.TabsBase(c.Key("tabs"), &chosen, len(libraryTabs))
		tabs.List.Gap(th.Space(2)).Label(locales.NavLibrary()).OnChange(func() {
			a.router.Push(libraryPath(libraryTab(chosen))) //nolint:gosec // an index into libraryTabs
		})
		tabs.List.Children(func() {
			for i, t := range libraryTabs {
				chip(c, tabs.Tab(i), t.name(), libraryTab(i) == tab)
			}
		})
	})
}

func (a *app) libraryPage(c *ui.Context, w float32, tab libraryTab) {
	header := func() { a.libraryHeader(c, tab) }
	if a.libraryState(c, w, header) {
		return
	}
	// Keyed by tab, each tab has its own list and keeps its scroll position
	// while another shows. An int: MyGo formats other key types every build.
	c.Key(int(tab))
	list := &a.page.library[tab]
	lib := a.session.Library()
	switch tab {
	case tabSongs:
		a.songList(c, list, lib.Songs, true, w, th.Space(1), header)
	case tabPlaylists:
		playlists, cards := a.cat.LibraryPlaylists(), a.playlistCards()
		cardGrid(c, list, w, 1+len(playlists), header, func(i int) {
			if i == 0 {
				a.likedCard(c)
				return
			}
			pl := &playlists[i-1]
			if mediaCard(c, cards[i-1].key, pl.Art, pl.Name, cards[i-1].sub, 0, false).Clicked() {
				a.router.Push(playlistPath(pl.ID))
			}
		})
	case tabAlbums:
		cards := a.albumCards()
		cardGrid(c, list, w, len(lib.Albums), header, func(i int) {
			al := &lib.Albums[i]
			if mediaCard(c, cards[i].key, al.Art, al.Title, cards[i].sub, 0, false).Clicked() {
				a.router.Push(albumPath(al.ID))
			}
		})
	case tabArtists:
		cards := a.artistCards()
		cardGrid(c, list, w, len(lib.Artists), header, func(i int) {
			ar := &lib.Artists[i]
			mediaCard(c, cards[i].key, ar.Art, ar.Name, cards[i].sub, 0, true)
		})
	}
}

func (a *app) likedCard(c *ui.Context) {
	sub := locales.AutoPlaylist()
	if a.feedState(session.LikedRes).State == fetchstate.Loaded {
		sub = a.likedLine()
	}
	if mediaCard(c, "liked", artLiked, locales.LikedMusic(), sub, 0, false).Clicked() {
		a.router.Push(pathLiked)
	}
}

func (a *app) likedLine() string {
	m := &a.memo.likedLine
	if m.stale(a.session.LikesGen()) {
		m.v = locales.AutoPlaylistSongs(a.likedCount())
	}
	return m.v
}

func (a *app) playlistCards() []cardText {
	m := &a.memo.libPlaylists
	if m.stale(a.cat.Gen()) {
		playlists := a.cat.LibraryPlaylists()
		m.v = make([]cardText, len(playlists))
		for i, pl := range playlists {
			m.v[i] = cardText{key: pl.ID, sub: playlistLine(pl, true)}
		}
	}
	return m.v
}

func (a *app) albumCards() []cardText {
	albums := a.session.Library().Albums
	m := &a.memo.libAlbums
	if m.stale(keyOf(albums)) {
		m.v = make([]cardText, len(albums))
		for i, al := range albums {
			sub := al.Artist
			if al.Year > 0 {
				sub = al.Artist + " · " + strconv.Itoa(al.Year)
			}
			m.v[i] = cardText{key: al.ID, sub: sub}
		}
	}
	return m.v
}

func (a *app) artistCards() []cardText {
	artists := a.session.Library().Artists
	m := &a.memo.libArtists
	if m.stale(keyOf(artists)) {
		m.v = make([]cardText, len(artists))
		for i, ar := range artists {
			m.v[i] = cardText{key: ar.Key(), sub: locales.Artist()}
		}
	}
	return m.v
}

// playlistLine shows the song count once known, else the owner.
func playlistLine(pl catalog.Playlist, withCount bool) string {
	switch {
	case withCount && len(pl.Songs) > 0:
		return locales.PlaylistSongs(len(pl.Songs))
	case pl.Owner != "":
		return playlistBy.say1(pl.Owner)
	}
	return locales.Playlist()
}

// cardGrid is a list of grid rows, so only those in view are built. Not a
// ui.GridView: it wants fixed-height cells, while a card grows with its width
// and two lines of text, and it cannot scroll the header with the cards.
func cardGrid(c *ui.Context, state *ui.ListState, w float32, count int, header func(), card func(i int)) {
	cols := columnsFor(w, th.Layout.Sized().Cell, 2, 7)
	pageList(c, state, th.Space(1), (count+cols-1)/cols, nil, header, func(row int) {
		grid := ui.Grid(c).Columns(cols).GapX(th.Space(5)).Padding(0, rowBleed)
		if row > 0 {
			grid.Margin(th.Space(7), 0, 0, 0)
		}
		grid.Children(func() {
			for i := row * cols; i < min(count, (row+1)*cols); i++ {
				card(i)
			}
		})
	})
}
