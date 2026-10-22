package main

import (
	"net/url"
	"strings"
	"yt-music-player/internal/fetchstate"
	"yt-music-player/internal/loader"
)

var idRoutes = [...]struct {
	prefix string
	kind   fetchstate.Kind
}{
	{albumPrefix, fetchstate.Album},
	{playlistPrefix, fetchstate.Playlist},
}

// resourceAt returns what a router location fetches, or the zero
// Resource. IDs come from the escaped path, as in Route.Param, so an
// escaped slash stays in the ID.
func resourceAt(loc string) fetchstate.Resource {
	u, err := url.Parse(loc)
	if err != nil {
		return fetchstate.Resource{}
	}
	path := u.EscapedPath()
	switch path {
	case pathHome, "/":
		return loader.HomeRes
	case pathExplore:
		return loader.ExploreRes
	}
	for _, route := range idRoutes {
		rest, ok := strings.CutPrefix(path, route.prefix)
		if !ok || rest == "" || strings.Contains(rest, "/") {
			continue
		}
		if id, err := url.PathUnescape(rest); err == nil {
			return fetchstate.Resource{Kind: route.kind, ID: id}
		}
	}
	return fetchstate.Resource{}
}

// syncLoad tells the loader what the location shows, however the router
// got there.
func (a *app) syncLoad() {
	if loc := a.router.Location(); loc != a.seen.loc {
		a.seen.loc = loc
		a.loader.Show(resourceAt(loc))
		query := ""
		if a.path() == pathSearch {
			query = strings.TrimSpace(a.searchQuery())
		}
		a.loader.Search(query)
	}
}

// playPlaylist opens a playlist whose songs are not known yet (as from a
// feed), and plays it once fetched.
func (a *app) playPlaylist(id string) {
	if pl, ok := a.cat.Playlist(id); ok && len(pl.Songs) > 0 {
		a.playAll(pl.Songs)
		return
	}
	res := fetchstate.Resource{Kind: fetchstate.Playlist, ID: id}
	switch a.fetches.Get(res).State {
	case fetchstate.Idle, fetchstate.Loading, fetchstate.Failed:
		a.loader.OnLoad(res, func() {
			pl, _ := a.cat.Playlist(id)
			a.playAll(pl.Songs)
		})
	}
	a.router.Push(playlistPath(id))
}

// stopWork runs as the window closes: later results have nowhere to show.
// Pending settings still save.
func (a *app) stopWork() {
	a.loader.Stop()
	if a.art != nil {
		a.art.Close()
	}
	a.disconnect()
	a.look.scan.Stop()
	a.look.saver.Wait()
}
