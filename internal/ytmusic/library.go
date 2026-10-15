package ytmusic

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"yt-music-player/internal/catalog"
)

// maxLibraryItems caps each library part: at 25 to 100 items a page, a
// part costs at most a few dozen requests.
const maxLibraryItems = 2000

// maxLibraryPages bounds a part's requests even if empty pages keep
// giving new tokens.
const maxLibraryPages = 100

// The library's pages, as ytmusicapi's get_library_* browse them.
const (
	libraryPlaylists = "FEmusic_liked_playlists"
	libraryAlbums    = "FEmusic_liked_albums"
	// libraryArtists are the artists of saved songs, as in YouTube Music's
	// Library › Artists. Subscriptions (FEmusic_library_corpus_artists)
	// would add podcasters and miss artists saved without subscribing.
	libraryArtists = "FEmusic_library_corpus_track_artists"
	librarySongs   = "FEmusic_liked_videos"
)

// autoPlaylists are in every library: the UI shows Liked music on its
// own, and the player has no podcasts for Episodes for later.
var autoPlaylists = map[string]bool{catalog.LikedID: true, "SE": true}

// Library returns the saved playlists, albums, artists and songs, up to
// maxLibraryItems each. A part that fails is left empty; Library fails
// only when every part did, or when signed out.
func (c *Client) Library(ctx context.Context) (catalog.Library, error) {
	c.load()
	if !c.api.Auth.IsSignedIn() {
		return catalog.Library{}, fmt.Errorf("ytmusic: library: %w", catalog.ErrSignedOut)
	}
	var lib catalog.Library
	acceptKind := func(kind catalog.ItemKind, add func(catalog.Item)) func(row item) bool {
		return func(row item) bool {
			it, ok := parseItem(row)
			if !ok || it.Kind != kind || autoPlaylists[it.Playlist.ID] {
				return false
			}
			add(it)
			return true
		}
	}
	parts := [...]struct {
		browseID string
		add      func(row item) bool
	}{
		{libraryPlaylists, acceptKind(catalog.ItemPlaylist, func(it catalog.Item) {
			lib.Playlists = append(lib.Playlists, it.Playlist)
		})},
		{libraryAlbums, acceptKind(catalog.ItemAlbum, func(it catalog.Item) {
			lib.Albums = append(lib.Albums, it.Album)
		})},
		{libraryArtists, acceptKind(catalog.ItemArtist, func(it catalog.Item) {
			lib.Artists = append(lib.Artists, it.Artist)
		})},
		// The shuffle row heading the songs has no video, so parseItem skips it.
		{librarySongs, acceptKind(catalog.ItemSong, func(it catalog.Item) {
			lib.Songs = append(lib.Songs, it.Song)
		})},
	}
	var errs [len(parts)]error
	var wg sync.WaitGroup
	for i, p := range parts {
		wg.Go(func() { errs[i] = c.libraryPart(ctx, p.browseID, p.add) })
	}
	wg.Wait()
	for _, err := range errs {
		if errors.Is(err, catalog.ErrSignedOut) {
			return catalog.Library{}, err
		}
	}
	if !slices.Contains(errs[:], nil) {
		err := errors.Join(errors.New("ytmusic: library: every part failed"), errors.Join(errs[:]...))
		return catalog.Library{}, err
	}
	return lib, nil
}

// libraryPart gives add every row of a library page and its
// continuations until it accepted maxLibraryItems. A failed continuation
// ends the part early instead of failing it, unless ctx is done or the
// session ended.
func (c *Client) libraryPart(ctx context.Context, browseID string, add func(row item) bool) error {
	resp, err := c.signedCall(ctx, "browse", map[string]any{"browseId": browseID})
	if err != nil {
		return fmt.Errorf("ytmusic: library %s: %w", browseID, err)
	}
	page := libraryContents(resp)
	rows := page.rows()
	added := 0
	seen := map[string]bool{}
	for pages := 1; ; pages++ {
		for _, row := range rows {
			if added < maxLibraryItems && add(row) {
				added++
			}
		}
		// Grids and shelves still page the old way; newer pages end their
		// rows with a continuation item.
		token := page.next()
		if token == "" {
			token = continuation(rows)
		}
		isCapped := added >= maxLibraryItems || pages >= maxLibraryPages
		if token == "" || seen[token] || isCapped {
			return nil
		}
		seen[token] = true
		next, err := c.signedCall(ctx, "browse", map[string]any{"continuation": token})
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, catalog.ErrSignedOut) {
				return fmt.Errorf("ytmusic: library %s: after %d items: %w", browseID, added, err)
			}
			return nil
		}
		page = cmp.Or(next.ContinuationContents.Grid, next.ContinuationContents.Shelf)
		rows = page.rows()
		if page == nil {
			rows = continuationItems(next)
		}
	}
}

// libraryContents is a library page's grid or shelf, on its own or in
// an item section. An empty library has neither, only a message.
func libraryContents(resp *response) *shelf {
	if resp.Contents == nil {
		return nil
	}
	for _, tab := range resp.Contents.SingleColumn.Tabs {
		for _, sec := range tab.Renderer.Content.SectionList.sections() {
			if found := cmp.Or(sec.Grid, sec.Shelf); found != nil {
				return found
			}
			if sec.ItemSection == nil {
				continue
			}
			for _, v := range sec.ItemSection.Contents {
				if found := cmp.Or(v.Grid, v.Shelf); found != nil {
					return found
				}
			}
		}
	}
	return nil
}
