// Package loader fetches what pages show (albums, playlists, the home and
// explore feeds, search results) from a Source. Only the shown page
// fetches, each resource once per session; leaving a page cancels its
// fetch and drops its result.
//
// A Loader belongs to the UI thread, which owns the catalog. Fetches run
// on goroutines that call only the Source; results are stored on the UI
// thread through post.
package loader

import (
	"context"
	"errors"
	"slices"
	"time"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/fetchstate"
	"yt-music-player/internal/job"

	"github.com/rs/zerolog/log"
)

// Feed resources; feeds have no ID.
var (
	HomeFeed    = fetchstate.Resource{Kind: fetchstate.Home}
	ExploreFeed = fetchstate.Resource{Kind: fetchstate.Explore}
)

// SearchDebounce is how long typing must pause before a search, so a word
// sends one request rather than one per letter.
const SearchDebounce = 250 * time.Millisecond

// Found is the state of search: the last query that completed with its
// Results, and the last query that failed with its Err.
type Found struct {
	Query       string
	Results     catalog.Results
	FailedQuery string
	Err         error
}

// Source is where the pages' music comes from: a catalog.Source, or ytmd
// through remote.Client. Methods are called off the UI thread.
type Source interface {
	Search(ctx context.Context, query string) (catalog.Results, error)
	Album(ctx context.Context, id string) (catalog.Album, []catalog.Song, error)
	Playlist(ctx context.Context, id string) (catalog.Playlist, []catalog.Song, error)
	Home(ctx context.Context) (catalog.Home, error)
	Explore(ctx context.Context) (catalog.Explore, error)
}

// Loader fetches the pages' resources and searches.
type Loader struct {
	src    Source
	states *fetchstate.Registry
	cat    *catalog.Catalog
	post   func(func())

	// Debounce is how long Search waits for typing to pause. Tests
	// lengthen it because the race detector slows typing past it.
	Debounce time.Duration

	// pageJob fetches shown, the shown page's resource.
	shown   fetchstate.Resource
	pageJob job.Job
	onLoad  loadCallback
	home    catalog.Home
	explore catalog.Explore

	// searchJob runs the debounce, then the source's search for
	// pendingQuery. wanted is the search page's query, "" off it.
	searchJob    job.Job
	pendingQuery string
	wanted       string
	found        Found
}

type loadCallback struct {
	res fetchstate.Resource
	fn  func()
}

// New returns a Loader that fetches from src into cat and records each
// fetch in states. post runs a func on the UI thread; it is first called
// only once the app shows or searches something.
func New(src Source, states *fetchstate.Registry, cat *catalog.Catalog, post func(func())) *Loader {
	return &Loader{src: src, states: states, cat: cat, post: post, Debounce: SearchDebounce}
}

// Show fetches res, the shown page's resource (zero for none), and
// cancels the fetch of the page left. A failed resource is fetched again.
func (l *Loader) Show(res fetchstate.Resource) {
	if res != l.shown {
		l.stopPage()
		l.shown = res
	}
	if res == (fetchstate.Resource{}) {
		return
	}
	if state := l.states.State(res).State; state == fetchstate.Idle || state == fetchstate.Failed {
		l.Retry(res)
	}
}

// Retry refetches res, the shown page's resource.
func (l *Loader) Retry(res fetchstate.Resource) {
	l.stopPage()
	l.shown = res
	l.states.Set(res, fetchstate.Fetch{State: fetchstate.Loading})
	src := l.src
	job.Run(&l.pageJob, l.post,
		func(ctx context.Context) (func(*Loader), error) { return fetch(ctx, src, res) },
		func(keep func(*Loader), err error) { l.finish(res, keep, err) })
}

// RetryFailed fetches again what failed and is still shown: the page's
// resource and the search page's query, as when the computer is back
// online.
func (l *Loader) RetryFailed() {
	if l.shown != (fetchstate.Resource{}) && l.states.State(l.shown).State == fetchstate.Failed {
		l.Retry(l.shown)
	}
	if l.found.Err != nil && l.found.FailedQuery == l.wanted {
		l.RetrySearch(l.wanted)
	}
}

// Forget makes res refetch: now if its page is shown, else when it next
// is.
func (l *Loader) Forget(res fetchstate.Resource) {
	if res == l.shown {
		l.Retry(res)
		return
	}
	l.states.Set(res, fetchstate.Fetch{})
}

// OnLoad runs fn once res loads, by the fetch in flight or the next,
// unless that fetch fails or is canceled. Only one fn waits: a later call
// replaces it, whatever its resource, because only the last clicked
// playlist should play.
func (l *Loader) OnLoad(res fetchstate.Resource, fn func()) { l.onLoad = loadCallback{res, fn} }

// fetch runs off the UI thread; keep stores the result on the UI thread.
func fetch(ctx context.Context, src Source, res fetchstate.Resource) (keep func(*Loader), err error) {
	switch res.Kind {
	case fetchstate.Album:
		album, tracks, err := src.Album(ctx, res.ID)
		return func(l *Loader) { l.cat.AddAlbum(album, tracks) }, err
	case fetchstate.Playlist:
		playlist, songs, err := src.Playlist(ctx, res.ID)
		return func(l *Loader) { l.cat.Add(songs, nil, []catalog.Playlist{playlist}) }, err
	case fetchstate.Home:
		home, err := src.Home(ctx)
		return func(l *Loader) { l.KeepHome(home) }, err
	case fetchstate.Explore:
		explore, err := src.Explore(ctx)
		return func(l *Loader) { l.KeepExplore(explore) }, err
	default:
		return nil, errors.New("loader: nothing to fetch")
	}
}

func (l *Loader) finish(res fetchstate.Resource, keep func(*Loader), err error) {
	waiting := l.onLoad
	if waiting.res == res {
		l.onLoad = loadCallback{}
	}
	switch {
	case errors.Is(err, catalog.ErrNotFound):
		log.Debug().Stringer("kind", res.Kind).Str("id", res.ID).Msg("fetch found nothing")
		l.states.Set(res, fetchstate.Fetch{State: fetchstate.Missing})
	case err != nil:
		log.Debug().Stringer("kind", res.Kind).Str("id", res.ID).Err(err).Msg("fetch failed")
		l.states.Set(res, fetchstate.Fetch{State: fetchstate.Failed, Err: err})
	default:
		keep(l)
		l.states.Set(res, fetchstate.Fetch{State: fetchstate.Loaded})
		if waiting.res == res {
			waiting.fn()
		}
	}
}

// stopPage cancels the page's fetch in flight and forgets it, so the page
// refetches when shown again.
func (l *Loader) stopPage() {
	if !l.pageJob.IsRunning() {
		return
	}
	l.pageJob.Stop()
	l.states.Set(l.shown, fetchstate.Fetch{})
	if l.onLoad.res == l.shown {
		l.onLoad = loadCallback{}
	}
}

// Home returns the fetched home feed. A refetch replaces its slices;
// nothing changes them in place.
func (l *Loader) Home() *catalog.Home { return &l.home }

// Explore returns the fetched explore feed; see Home.
func (l *Loader) Explore() *catalog.Explore { return &l.explore }

// KeepHome stores home as the loaded home feed and adds its music to the
// catalog, so its items play and open like the rest. It drops repeated
// items: the UI keys items by Key, which must be unique among siblings.
func (l *Loader) KeepHome(home catalog.Home) {
	for i := range home.Shelves {
		home.Shelves[i].Items = Unique(home.Shelves[i].Items, func(item catalog.Item) string { return item.Key() })
	}
	l.home = home
	l.cat.Add(shelfMusic(home.Shelves))
	l.states.Set(HomeFeed, fetchstate.Fetch{State: fetchstate.Loaded})
}

// KeepExplore stores explore as the loaded explore feed; see KeepHome.
func (l *Loader) KeepExplore(explore catalog.Explore) {
	explore.Moods = Unique(explore.Moods, catalog.Mood.Key)
	explore.NewReleases = Unique(explore.NewReleases, func(album catalog.Album) string { return album.ID })
	explore.Trending = Unique(explore.Trending, func(song catalog.Song) string { return song.ID })
	l.explore = explore
	l.cat.Add(explore.Trending, explore.NewReleases, nil)
	l.states.Set(ExploreFeed, fetchstate.Fetch{State: fetchstate.Loaded})
}

// Unique removes items whose key repeats an earlier item's, in place.
func Unique[T any, K comparable](items []T, key func(T) K) []T {
	seen := make(map[K]struct{}, len(items))
	return slices.DeleteFunc(items, func(item T) bool {
		k := key(item)
		if _, ok := seen[k]; ok {
			return true
		}
		seen[k] = struct{}{}
		return false
	})
}

// shelfMusic returns new slices because the catalog may change them as it
// adds them.
func shelfMusic(shelves []catalog.Shelf) (songs []catalog.Song, albums []catalog.Album, playlists []catalog.Playlist) {
	// Counted first so each slice is allocated once; items are read in
	// place, as each holds every kind.
	var count [catalog.ItemArtist + 1]int
	for _, shelf := range shelves {
		for i := range shelf.Items {
			if kind := shelf.Items[i].Kind; int(kind) < len(count) {
				count[kind]++
			}
		}
	}
	songs = make([]catalog.Song, 0, count[catalog.ItemSong])
	albums = make([]catalog.Album, 0, count[catalog.ItemAlbum])
	playlists = make([]catalog.Playlist, 0, count[catalog.ItemPlaylist])
	for _, shelf := range shelves {
		for i := range shelf.Items {
			switch item := &shelf.Items[i]; item.Kind {
			case catalog.ItemSong:
				songs = append(songs, item.Song)
			case catalog.ItemAlbum:
				albums = append(albums, item.Album)
			case catalog.ItemPlaylist:
				playlists = append(playlists, item.Playlist)
			}
		}
	}
	return songs, albums, playlists
}

// Search follows the search page's trimmed query as it changes: it
// searches once typing pauses, and cancels a search no longer wanted
// ("" means no search page). A failed search stays failed until
// RetrySearch or the next Search.
func (l *Loader) Search(query string) {
	l.wanted = query
	switch {
	case query == "" || query == l.found.Query:
		l.searchJob.Stop()
	case l.searchJob.IsRunning() && query == l.pendingQuery:
	default:
		l.startSearch(query, l.Debounce)
	}
}

// RetrySearch searches for query without debounce.
func (l *Loader) RetrySearch(query string) { l.startSearch(query, 0) }

func (l *Loader) startSearch(query string, delay time.Duration) {
	l.pendingQuery = query
	l.found.FailedQuery, l.found.Err = "", nil
	src := l.src
	job.Run(&l.searchJob, l.post,
		func(ctx context.Context) (catalog.Results, error) { return debouncedSearch(ctx, src, query, delay) },
		func(results catalog.Results, err error) { l.finishSearch(query, results, err) })
}

func debouncedSearch(ctx context.Context, src Source, query string, delay time.Duration) (catalog.Results, error) {
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return catalog.Results{}, ctx.Err()
		case <-timer.C:
		}
	}
	return src.Search(ctx, query)
}

func (l *Loader) finishSearch(query string, results catalog.Results, err error) {
	if err != nil {
		log.Debug().Str("query", query).Err(err).Msg("search failed")
		l.found.FailedQuery, l.found.Err = query, err
		return
	}
	l.found.Query, l.found.Results = query, results
	// Playing a found song or opening a found album looks it up in the
	// catalog.
	l.cat.Add(results.Songs, results.Albums, nil)
}

// Found returns the state of search.
func (l *Loader) Found() Found { return l.found }

// IsBusy reports whether a fetch or a search is in flight.
func (l *Loader) IsBusy() bool { return l.pageJob.IsRunning() || l.searchJob.IsRunning() }

// Stop cancels the fetch and the search in flight; called as the window
// closes.
func (l *Loader) Stop() {
	l.stopPage()
	l.searchJob.Stop()
}
