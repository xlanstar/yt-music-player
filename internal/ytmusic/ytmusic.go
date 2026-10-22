// Package ytmusic reads YouTube Music through InnerTube into catalog
// types. Parsers follow ytmusicapi (github.com/sigma67/ytmusicapi).
package ytmusic

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"yt-music-player/internal/auth"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/innertube"
)

// Search filters, as ytmusicapi's get_search_params builds them.
const (
	paramsSongs  = "EgWKAQIIAWoMEA4QChADEAQQCRAF"
	paramsAlbums = "EgWKAQIYAWoMEA4QChADEAQQCRAF"
)

// maxPlaylistSongs caps a playlist fetch: past it, the requests (100
// songs each) cost more than a player gains.
const maxPlaylistSongs = 5000

var (
	_ catalog.Source   = (*Client)(nil)
	_ catalog.Accounts = (*Client)(nil)
)

// Client is a catalog.Source and catalog.Accounts backed by YouTube
// Music. It is safe for concurrent use.
type Client struct {
	api   *innertube.Client
	store auth.Store
	// load runs loadStored on first use, off the UI thread, since the
	// Keychain may prompt.
	load func()
	// mu serializes sign-in and sign-out, which pair a store write with a
	// credentials switch, and guards id.
	mu sync.Mutex
	// id is the sign-in's (auth.Credentials.ID), "" signed out.
	id string
}

// New returns a client sending requests through a copy of api (nil means
// innertube.New()). store keeps sign-ins across runs (nil: this session
// only); New does no I/O.
func New(api *innertube.Client, store auth.Store) *Client {
	if api == nil {
		api = innertube.New()
	}
	cp := *api
	if cp.Auth == nil {
		cp.Auth = &innertube.Auth{}
	}
	c := &Client{api: &cp, store: store}
	c.load = sync.OnceFunc(c.loadStored)
	return c
}

// call is signed when the client is signed in, so feeds are personal.
func (c *Client) call(ctx context.Context, endpoint string, body map[string]any) (*response, error) {
	c.load()
	return publicCall(ctx, c.api, endpoint, body)
}

// publicCall retries anonymously when the credentials are refused, so
// stale credentials never break browsing.
func publicCall(
	ctx context.Context,
	api *innertube.Client,
	endpoint string,
	body map[string]any,
) (*response, error) {
	resp, err := callWith(ctx, api, endpoint, body)
	if errors.Is(err, innertube.ErrUnauthorized) && api.Auth.IsSignedIn() {
		anon := *api
		anon.Auth = nil
		return callWith(ctx, &anon, endpoint, body)
	}
	return resp, err
}

// callWith's errors, as every helper's, name the endpoint but not the
// package: the exported method the caller called does, once.
func callWith(
	ctx context.Context,
	api *innertube.Client,
	endpoint string,
	body map[string]any,
) (*response, error) {
	var resp *response
	err := api.Call(ctx, endpoint, body, func(r io.Reader) (err error) {
		if resp, err = decode(r); err != nil {
			return fmt.Errorf("%s: %w", endpoint, err)
		}
		return nil
	})
	return resp, err
}

// caller is Client.call or, for what needs an account, Client.signedCall.
type caller func(ctx context.Context, endpoint string, body map[string]any) (*response, error)

// browse maps the ways InnerTube says an ID is unknown to ErrNotFound:
// 400 or 404, an error alert, or a page without contents.
func browse(ctx context.Context, call caller, what, id string, body map[string]any) (*response, error) {
	resp, err := call(ctx, "browse", body)
	switch se, isStatus := errors.AsType[*innertube.StatusError](err); {
	case errors.Is(err, catalog.ErrSignedOut):
		return nil, fmt.Errorf("ytmusic: %s %q: %w", what, id, err)
	case isStatus && (se.StatusCode == http.StatusNotFound || se.StatusCode == http.StatusBadRequest):
		return nil, fmt.Errorf("ytmusic: %s %q: %w: %w", what, id, catalog.ErrNotFound, err)
	case err != nil:
		return nil, fmt.Errorf("ytmusic: %s %q: %w", what, id, err)
	}
	if resp.Contents == nil {
		msg := alert(resp)
		if msg == "" {
			msg = "no contents"
		}
		return nil, fmt.Errorf("ytmusic: %s %q: %w (%s)", what, id, catalog.ErrNotFound, msg)
	}
	return resp, nil
}

func alert(resp *response) string {
	for _, entry := range resp.Alerts {
		if entry.Alert != nil && entry.Alert.Type == "ERROR" {
			return entry.Alert.Text.String()
		}
	}
	return ""
}

// Search finds songs and albums, with one filtered search for each.
func (c *Client) Search(ctx context.Context, query string) (catalog.Results, error) {
	var results catalog.Results
	query = strings.TrimSpace(query)
	if query == "" {
		return results, nil
	}
	var wg sync.WaitGroup
	var songErr, albumErr error
	wg.Go(func() {
		var resp *response
		resp, songErr = c.search(ctx, query, paramsSongs)
		results.Songs = parseRows(searchRows(resp), parseSong)
	})
	wg.Go(func() {
		var resp *response
		resp, albumErr = c.search(ctx, query, paramsAlbums)
		results.Albums = parseRows(searchRows(resp), parseAlbumRow)
	})
	wg.Wait()
	if err := errors.Join(songErr, albumErr); err != nil {
		return catalog.Results{}, err
	}
	return results, nil
}

func (c *Client) search(ctx context.Context, query, params string) (*response, error) {
	resp, err := c.call(ctx, "search", map[string]any{"query": query, "params": params})
	if err != nil {
		return nil, fmt.Errorf("ytmusic: search %q: %w", query, err)
	}
	return resp, nil
}

func parseRows[T any](rows []item, parse func(item) (T, bool)) []T {
	// Nearly every row parses: one allocation, not a doubling every few.
	out := slices.Grow([]T(nil), len(rows))
	for _, row := range rows {
		if v, ok := parse(row); ok {
			out = append(out, v)
		}
	}
	return out
}

// searchRows returns the rows of every shelf: the filter, not the shelf,
// decides what they are.
func searchRows(resp *response) []item {
	if resp == nil || resp.Contents == nil {
		return nil
	}
	sections := resp.Contents.TabbedSearch.first().sections()
	if sections == nil {
		sections = resp.Contents.SectionList.sections()
	}
	var rows []item
	for _, sec := range sections {
		if sec.Shelf != nil {
			rows = append(rows, sec.Shelf.Contents...)
		}
	}
	return rows
}

// Album returns an album, by its browse ID (MPREb_…), and its tracks.
func (c *Client) Album(ctx context.Context, id string) (catalog.Album, []catalog.Song, error) {
	if !strings.HasPrefix(id, "MPRE") {
		err := fmt.Errorf("ytmusic: album %q: %w (not an album browse id)", id, catalog.ErrNotFound)
		return catalog.Album{}, nil, err
	}
	resp, err := browse(ctx, c.call, "album", id, map[string]any{"browseId": id})
	if err != nil {
		return catalog.Album{}, nil, err
	}
	album, tracks, ok := albumPage(id, resp)
	if !ok {
		return catalog.Album{}, nil, fmt.Errorf("ytmusic: album %q: %w (no header)", id, catalog.ErrNotFound)
	}
	return album, tracks, nil
}

func albumPage(id string, resp *response) (catalog.Album, []catalog.Song, bool) {
	var h *header
	if sections := headerColumn(resp); len(sections) > 0 {
		h = sections[0].Header
	}
	if h == nil {
		h = resp.Header.Detail
	}
	if h == nil {
		return catalog.Album{}, nil, false
	}
	album := catalog.Album{ID: id, Title: h.Title.Runs.String(), Art: h.art()}
	album.Kind, album.Artist, album.Year = subtitle(h.Subtitle.Runs)
	var strapline meta
	strapline.read(h.StraplineTextOne.Runs, true)
	if a := strapline.artist(); a != "" {
		album.Artist = a
	}

	var tracks []catalog.Song
	for _, sec := range listColumn(resp) {
		if sec.Shelf == nil {
			continue
		}
		for _, row := range sec.Shelf.Contents {
			s, ok := parseSong(row)
			if !ok {
				continue
			}
			// Tracks by the album's artist leave theirs blank.
			if s.Artist == "" {
				s.Artist = album.Artist
			}
			s.Album, s.AlbumID = album.Title, album.ID
			if s.Art == "" {
				s.Art = album.Art
			}
			tracks = append(tracks, s)
		}
	}
	return album, tracks, true
}

func headerColumn(resp *response) []section {
	if resp.Contents == nil {
		return nil
	}
	return resp.Contents.TwoColumn.first().sections()
}

// listColumn is a two-column page's list column, or the only column of
// the single-column pages YouTube served before 2024.
func listColumn(resp *response) []section {
	if resp.Contents == nil {
		return nil
	}
	if s := resp.Contents.TwoColumn.Secondary.SectionList.sections(); s != nil {
		return s
	}
	return sectionList(resp).sections()
}

// sectionList is the only column of a single-column browse page, which
// feeds (home, explore, charts, moods) still are.
func sectionList(resp *response) *sectionListRenderer {
	if resp.Contents == nil {
		return nil
	}
	return resp.Contents.SingleColumn.first()
}

// Playlist returns a playlist, by its ID without "VL", and up to
// maxPlaylistSongs of its songs. catalog.LikedID needs an account and
// returns catalog.ErrSignedOut without one.
func (c *Client) Playlist(ctx context.Context, id string) (catalog.Playlist, []catalog.Song, error) {
	id = strings.TrimPrefix(id, "VL")
	if id == "" {
		return catalog.Playlist{}, nil, fmt.Errorf("ytmusic: playlist %q: %w (empty id)", id, catalog.ErrNotFound)
	}
	call := c.call
	if id == catalog.LikedID {
		call = c.signedCall
	}
	resp, err := browse(ctx, call, "playlist", id, map[string]any{"browseId": "VL" + id})
	if err != nil {
		return catalog.Playlist{}, nil, err
	}
	playlist, rows, ok := playlistPage(id, resp)
	if !ok {
		err := fmt.Errorf("ytmusic: playlist %q: %w (no header or songs)", id, catalog.ErrNotFound)
		return catalog.Playlist{}, nil, err
	}
	var songs []catalog.Song
	seen := map[string]bool{}
	for {
		songs = playlistSongs(songs, rows)
		token := continuation(rows)
		if token == "" || seen[token] || len(songs) >= maxPlaylistSongs {
			break
		}
		seen[token] = true
		next, err := call(ctx, "browse", map[string]any{"continuation": token})
		if err != nil {
			err = fmt.Errorf("ytmusic: playlist %q: after %d songs: %w", id, len(songs), err)
			return catalog.Playlist{}, nil, err
		}
		rows = continuationItems(next)
	}
	playlist.Songs = make([]string, len(songs))
	for i, s := range songs {
		if s.Art == "" {
			s.Art = playlist.Art
			songs[i] = s
		}
		playlist.Songs[i] = s.ID
	}
	return playlist, songs, nil
}

// playlistPage returns the playlist without its songs, and the song rows
// of its first page.
func playlistPage(id string, resp *response) (catalog.Playlist, []item, bool) {
	var h *header
	if sections := headerColumn(resp); len(sections) > 0 {
		h = cmp.Or(sections[0].Header, sections[0].EditableHeader.Header.Header)
	}
	h = cmp.Or(h, resp.Header.Detail)
	var playlistShelf *shelf
	for _, sec := range listColumn(resp) {
		if playlistShelf = sec.PlaylistShelf; playlistShelf != nil {
			break
		}
	}
	if h == nil && playlistShelf == nil {
		return catalog.Playlist{}, nil, false
	}
	if h == nil {
		h = &header{}
	}
	playlist := catalog.Playlist{ID: id, Name: h.Title.Runs.String(), Owner: owner(h), Art: h.art()}
	if playlistShelf == nil {
		return playlist, nil, true
	}
	return playlist, playlistShelf.Contents, true
}

func playlistSongs(songs []catalog.Song, rows []item) []catalog.Song {
	songs = slices.Grow(songs, min(len(rows), maxPlaylistSongs-len(songs)))
	for _, row := range rows {
		if len(songs) == maxPlaylistSongs {
			break
		}
		if s, ok := parseSong(row); ok {
			songs = append(songs, s)
		}
	}
	return songs
}

// continuationItems are the rows of a newer-style continuation page.
func continuationItems(resp *response) []item {
	if len(resp.ReceivedActions) == 0 {
		return nil
	}
	return resp.ReceivedActions[0].Append.Items
}

// owner is the playlist's author: a facepile since 2024, before that a
// strapline or a linked subtitle run.
func owner(h *header) string {
	if o := h.Facepile.Stack.Text.Content; o != "" {
		return o
	}
	if o := h.StraplineTextOne.Runs.String(); o != "" {
		return o
	}
	var m meta
	m.read(h.Subtitle.Runs, false)
	return m.artist()
}

// continuation is the next page's token, from the last row.
func continuation(rows []item) string {
	if len(rows) == 0 || rows[len(rows)-1].Continuation == nil {
		return ""
	}
	ep := &rows[len(rows)-1].Continuation.Endpoint
	if ep.Command != nil && ep.Command.Token != "" {
		return ep.Command.Token
	}
	// Some pages wrap the command with others, like a voting refresh.
	for _, cmd := range ep.Executor.Commands {
		if cmd.Command != nil && cmd.Command.Request == "CONTINUATION_REQUEST_TYPE_BROWSE" {
			return cmd.Command.Token
		}
	}
	return ""
}
