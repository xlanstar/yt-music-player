package main

import (
	"net/url"
	"strings"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/fetchstate"
	"yt-music-player/locales"

	"github.com/egoist/mygo/ui"
)

// Pages are router paths, so ui.Router provides Back and Forward (keys and
// mouse buttons), scroll restoration and focus. Search is a page too, so
// Back leaves it like any other.
const (
	pathHome       = "/home"
	pathExplore    = "/explore"
	pathLibrary    = "/library" // ?tab=songs|albums|artists; none for playlists
	pathLiked      = "/liked"
	pathSearch     = "/search"
	albumPrefix    = "/album/"
	playlistPrefix = "/playlist/"
)

func albumPath(id string) string    { return albumPrefix + url.PathEscape(id) }
func playlistPath(id string) string { return playlistPrefix + url.PathEscape(id) }
func searchPath(q string) string    { return pathSearch + "?q=" + url.QueryEscape(q) }

// The routes, in the order of routePatterns.
const (
	routeHome = iota
	routeRoot
	routeExplore
	routeLibrary
	routeLiked
	routeAlbum
	routePlaylist
	routeSearch
	routeCount
)

// routePatterns match one path each, so the order they are tried in
// changes no outcome.
var routePatterns = [routeCount]string{
	pathHome, "/", pathExplore, pathLibrary, pathLiked, albumPrefix + "{id}", playlistPrefix + "{id}", pathSearch,
}

// pages builds the routed page, one scroller whose position the router
// restores; pages whose rows run into thousands are lists.
func (a *app) pages(c *ui.Context, width float32) {
	a.router.View(c, func(r *ui.Route) {
		switch a.route(r) {
		case routeHome, routeRoot:
			r.Title(locales.NavHome())
			scrollPage(c, func() { a.homePage(c, width) })
		case routeExplore:
			r.Title(locales.NavExplore())
			scrollPage(c, func() { a.explorePage(c, width) })
		case routeLibrary:
			r.Title(locales.NavLibrary())
			a.libraryPage(c, width, libraryTabOf(r.Query("tab")))
		case routeLiked:
			r.Title(locales.LikedMusic())
			a.likedPage(c, width)
		case routeAlbum:
			a.itemPage(c, width, r, fetchstate.Resource{Kind: fetchstate.Album, ID: r.Param("id")})
		case routePlaylist:
			a.itemPage(c, width, r, fetchstate.Resource{Kind: fetchstate.Playlist, ID: r.Param("id")})
		case routeSearch:
			r.Title(locales.NavSearch())
			scrollPage(c, func() { a.searchPage(c, width, r.Query("q")) })
		default:
			notFound(c)
		}
	})
}

// route is the route r matches, -1 for none. Each Match allocates, so
// the one the current path names goes first. r may be a page leaving,
// built at its own location, which is also why pages read r.Query.
func (a *app) route(r *ui.Route) int {
	hint := routeOf(a.path())
	if hint >= 0 && r.Match(routePatterns[hint]) {
		return hint
	}
	for i, pattern := range routePatterns {
		if i != hint && r.Match(pattern) {
			return i
		}
	}
	return -1
}

// routeOf guesses the route of path, -1 for none; route checks it.
func routeOf(path string) int {
	switch path {
	case pathHome:
		return routeHome
	case "/":
		return routeRoot
	case pathExplore:
		return routeExplore
	case pathLibrary:
		return routeLibrary
	case pathLiked:
		return routeLiked
	case pathSearch:
		return routeSearch
	}
	switch {
	case strings.HasPrefix(path, albumPrefix):
		return routeAlbum
	case strings.HasPrefix(path, playlistPrefix):
		return routePlaylist
	}
	return -1
}

// scrollPage is for pages of a few sections; long ones use pageList.
func scrollPage(c *ui.Context, page func()) {
	ui.Scroll(c).Grow(1).ScrollbarInsets(0, 0, th.Layout.PanelRadius).Children(page)
}

// message fills a page with nothing else to show. icon, body and more
// are optional; more adds a way out below.
func message(c *ui.Context, icon *ui.SVG, title, body string, level int, more func()) {
	ui.Column(c).Padding(th.Space(24), th.Layout.Page).Gap(th.Space(2.5)).AlignItems(ui.Center).Children(func() {
		if icon != nil {
			badge := ui.Box(c).Size(72, 72).Radius(36).Center().Background(th.Color.Raised).Margin(0, 0, th.Space(2), 0)
			badge.Children(func() { ui.Icon(c, icon).Size(28, 28).TextColor(th.Color.Muted) })
		}
		heading(c, title, th.Type.Message, level).TextAlign(ui.Center)
		if body != "" {
			ui.Text(c, body).TextColor(th.Color.Muted).TextAlign(ui.Center).MaxWidth(520).MaxLines(3)
		}
		if more != nil {
			more()
		}
	})
}

func notFound(c *ui.Context) {
	message(c, nil, locales.NotFoundTitle(), locales.NotFoundDetail(), 1, nil)
}

func loadFailed(c *ui.Context, title string, err error, retry func()) {
	message(c, nil, title, err.Error(), 1, func() {
		if pill(c, "retry", nil, locales.Retry(), pillLight).Margin(th.Space(3), 0, 0, 0).Clicked() {
			retry()
		}
	})
}

// onPath reports whether the page shown is at path, or under it when path
// ends in a slash.
func (a *app) onPath(path string) bool {
	current := a.path()
	if strings.HasSuffix(path, "/") {
		return strings.HasPrefix(current, path)
	}
	isHomeAtRoot := path == pathHome && current == "/"
	return current == path || isHomeAtRoot
}

// glowArt tints the top of the main panel: the page's art, else the current
// song's.
func (a *app) glowArt() catalog.Art {
	current := a.path()
	if id, ok := strings.CutPrefix(current, albumPrefix); ok {
		if al, ok := a.cat.Album(id); ok {
			return al.Art
		}
	}
	if id, ok := strings.CutPrefix(current, playlistPrefix); ok {
		if pl, ok := a.cat.Playlist(id); ok {
			return pl.Art
		}
	}
	return a.currentSong().Art
}

// syncQuery copies the search page's query into the field when the router
// moves (as by Back) and clears it elsewhere. searchFor records the locations
// typing makes, so they are not copied back.
func (a *app) syncQuery() {
	loc := a.router.Location()
	if loc == a.search.loc {
		return
	}
	a.search.loc = loc
	a.search.query = ""
	if a.path() == pathSearch {
		a.search.query = a.searchQuery()
	}
}

// searchFor pushes the search page on the first keystroke and replaces it
// after, so Back leaves search rather than undoing one letter at a time.
func (a *app) searchFor(q string) {
	switch {
	case strings.TrimSpace(q) == "":
		a.leaveSearch()
		return
	case a.path() == pathSearch:
		a.router.Replace(searchPath(q))
	default:
		a.router.Push(searchPath(q))
	}
	a.search.loc = a.router.Location()
}

func (a *app) leaveSearch() {
	if a.path() != pathSearch {
		return
	}
	if a.router.CanGoBack() {
		a.router.Back()
		return
	}
	a.router.Replace(pathHome)
}
