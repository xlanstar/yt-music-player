package ytmusic

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"yt-music-player/internal/catalog"

	"github.com/rs/zerolog/log"
)

// maxHomeContinuations caps the pages after the home feed's first: each
// brings a few shelves, and scrolling that far is rare.
const maxHomeContinuations = 3

const (
	homeBrowseID        = "FEmusic_home"
	newReleasesBrowseID = "FEmusic_new_releases_albums"
)

// Home returns the home feed's shelves: personal when signed in, else
// those a visitor from the IP's region sees. A failed continuation ends
// the feed early instead of failing it, unless ctx is done.
func (c *Client) Home(ctx context.Context) (catalog.Home, error) {
	resp, err := c.call(ctx, "browse", map[string]any{"browseId": homeBrowseID})
	if err != nil {
		return catalog.Home{}, fmt.Errorf("ytmusic: home: %w", err)
	}
	list := sectionList(resp)
	shelves := parseShelves(list.sections())
	// Continuations belong to the visitor InnerTube made up for the first
	// page; asked as anyone else, they come back empty.
	api := c.api
	if visitor := resp.ResponseContext.VisitorData; visitor != "" && api.VisitorID == "" {
		cp := *c.api
		cp.VisitorID = visitor
		api = &cp
	}
	seen := map[string]bool{}
	for range maxHomeContinuations {
		token := list.next()
		if token == "" || seen[token] {
			break
		}
		seen[token] = true
		next, err := publicCall(ctx, api, "browse", map[string]any{"continuation": token})
		if err != nil {
			if ctx.Err() != nil {
				return catalog.Home{}, fmt.Errorf("ytmusic: home: %w", err)
			}
			log.Debug().Str("browseId", homeBrowseID).Int("shelves", len(shelves)).Err(err).
				Msg("ytmusic: a continuation failed; the feed ends early")
			break
		}
		list = next.ContinuationContents.SectionList
		shelves = append(shelves, parseShelves(list.sections())...)
	}
	if len(shelves) == 0 {
		return catalog.Home{}, errors.New("ytmusic: home: no shelves")
	}
	return catalog.Home{Shelves: shelves}, nil
}

// parseShelves skips sections that are not carousels with items, like
// the taste builder shown to new visitors.
func parseShelves(sections []section) []catalog.Shelf {
	var shelves []catalog.Shelf
	for _, sec := range sections {
		if s, ok := parseShelf(sec.Carousel); ok {
			shelves = append(shelves, s)
		}
	}
	return shelves
}

func parseShelf(sh *shelf) (catalog.Shelf, bool) {
	if sh == nil {
		return catalog.Shelf{}, false
	}
	header := &sh.Header.Basic
	s := catalog.Shelf{Title: header.Title.Runs.String(), Strapline: header.Strapline.Runs.String()}
	// Some headers, like "More from", carry only a strapline.
	if s.Title == "" {
		s.Title, s.Strapline = s.Strapline, ""
	}
	for _, v := range sh.Contents {
		if it, ok := parseItem(v); ok {
			s.Items = append(s.Items, it)
		}
	}
	return s, len(s.Items) > 0
}

// parseItem reads a carousel entry: a two-row card or a list row. It
// skips what the player cannot open (podcasts, episodes, radios).
func parseItem(v item) (catalog.Item, bool) {
	if v.Card != nil {
		return parseCard(v.Card)
	}
	if s, ok := parseSong(v); ok {
		return catalog.Item{Kind: catalog.ItemSong, Song: s}, true
	}
	row := v.ListItem
	if row == nil {
		return catalog.Item{}, false
	}
	return browseItem(row.NavigationEndpoint.Browse, row.column(0).String(), row.art(), row.column(1))
}

func parseCard(c *card) (catalog.Item, bool) {
	title := c.Title.Runs.String()
	ep := &c.NavigationEndpoint
	if ep.Browse != nil {
		return browseItem(ep.Browse, title, c.art(), c.Subtitle.Runs)
	}
	if id := ep.Watch.VideoID; id != "" && title != "" {
		var m meta
		m.read(skipLabel(c.Subtitle.Runs, typeLabels), true)
		return catalog.Item{Kind: catalog.ItemSong, Song: catalog.Song{
			ID:      id,
			Title:   title,
			Artist:  m.artist(),
			Album:   m.album,
			AlbumID: m.albumID,
			Seconds: m.seconds,
			Art:     c.art(),
		}}, true
	}
	if id := ep.WatchPlaylist.PlaylistID; isMix(id) && title != "" {
		return catalog.Item{Kind: catalog.ItemPlaylist, Playlist: catalog.Playlist{
			ID: id, Name: title, Owner: playlistOwner(c.Subtitle.Runs), Art: c.art(),
		}}, true
	}
	return catalog.Item{}, false
}

// isMix reports generated playlists that can be browsed like any other,
// unlike a song's radio (RDAMVM…).
func isMix(id string) bool {
	return strings.HasPrefix(id, "RDCLAK") || strings.HasPrefix(id, "RDAT") || strings.HasPrefix(id, "RDTMAK")
}

func browseItem(endpoint *browseEndpoint, title string, thumb catalog.Art, subtitleRuns runs) (catalog.Item, bool) {
	id, page := endpoint.target()
	if id == "" || title == "" {
		return catalog.Item{}, false
	}
	switch {
	case strings.HasPrefix(id, "MPRE"):
		album := catalog.Album{ID: id, Title: title, Art: thumb}
		album.Kind, album.Artist, album.Year = subtitle(subtitleRuns)
		return catalog.Item{Kind: catalog.ItemAlbum, Album: album}, true
	case strings.HasPrefix(id, "VL") && (page == "" || page == "MUSIC_PAGE_TYPE_PLAYLIST"):
		return catalog.Item{Kind: catalog.ItemPlaylist, Playlist: catalog.Playlist{
			ID: strings.TrimPrefix(id, "VL"), Name: title, Owner: playlistOwner(subtitleRuns), Art: thumb,
		}}, true
	case strings.HasPrefix(id, "UC") && (page == "" || page == "MUSIC_PAGE_TYPE_ARTIST"):
		return catalog.Item{Kind: catalog.ItemArtist, Artist: catalog.Artist{ID: id, Name: title, Art: thumb}}, true
	}
	return catalog.Item{}, false
}

// playlistOwner is what a card says under a playlist: its linked author
// or, for YouTube Music's own playlists, the artists after any label.
func playlistOwner(rs runs) string {
	var linked meta
	for _, r := range rs {
		if r.NavigationEndpoint != nil {
			linked.readLink(strings.TrimSpace(r.Text), r.NavigationEndpoint.Browse)
		}
	}
	if o := linked.artist(); o != "" {
		return o
	}
	var m meta
	m.read(skipLabel(rs, playlistLabels), true)
	return m.artist()
}

// Explore returns new releases, the chart and moods. Each part fails
// alone: Explore errs only when none parsed.
func (c *Client) Explore(ctx context.Context) (catalog.Explore, error) {
	var ex catalog.Explore
	var exploreErr, chartErr, moodsErr error
	var wg sync.WaitGroup
	wg.Go(func() {
		ex.NewReleases, ex.Trending, exploreErr = c.explore(ctx)
		if len(ex.Trending) == 0 {
			ex.Trending, chartErr = c.chart(ctx)
		}
	})
	wg.Go(func() { ex.Moods, moodsErr = c.moods(ctx) })
	wg.Wait()
	if len(ex.NewReleases) == 0 && len(ex.Trending) == 0 && len(ex.Moods) == 0 {
		err := errors.Join(errors.New("ytmusic: explore: nothing parsed"), exploreErr, chartErr, moodsErr)
		return catalog.Explore{}, err
	}
	return ex, nil
}

func (c *Client) explore(ctx context.Context) ([]catalog.Album, []catalog.Song, error) {
	resp, err := c.call(ctx, "browse", map[string]any{"browseId": "FEmusic_explore"})
	if err != nil {
		return nil, nil, fmt.Errorf("ytmusic: explore: %w", err)
	}
	albums, trending := explorePage(resp)
	return albums, trending, nil
}

// explorePage finds shelves by the page their title links to, since
// titles change with the language: new releases by browse ID, the chart
// (trending, or top songs in some regions) as a playlist of song rows.
func explorePage(resp *response) (albums []catalog.Album, trending []catalog.Song) {
	for _, sec := range sectionList(resp).sections() {
		sh := sec.Carousel
		if sh == nil {
			continue
		}
		var id string
		if title := sh.Header.Basic.Title.Runs; len(title) > 0 && title[0].NavigationEndpoint != nil {
			id, _ = title[0].NavigationEndpoint.Browse.target()
		}
		switch {
		case id == newReleasesBrowseID && albums == nil:
			for _, v := range sh.Contents {
				if it, ok := parseItem(v); ok && it.Kind == catalog.ItemAlbum {
					albums = append(albums, it.Album)
				}
			}
		case strings.HasPrefix(id, "VL") && trending == nil:
			trending = ranked(sh.Contents)
		}
	}
	return albums, trending
}

// ranked returns the songs among rows by chart rank; unranked ones
// follow in order.
func ranked(rows []item) []catalog.Song {
	type entry struct {
		rank int
		song catalog.Song
	}
	var entries []entry
	for _, row := range rows {
		s, ok := parseSong(row)
		if !ok {
			continue
		}
		index := row.ListItem.CustomIndexColumn.Renderer.Text.Runs.String()
		rank, err := strconv.Atoi(strings.TrimSpace(index))
		if err != nil || rank < 1 {
			rank = math.MaxInt
		}
		entries = append(entries, entry{rank: rank, song: s})
	}
	slices.SortStableFunc(entries, func(a, b entry) int { return cmp.Compare(a.rank, b.rank) })
	songs := make([]catalog.Song, len(entries))
	for i, e := range entries {
		songs[i] = e.song
	}
	return songs
}

// chart reads the song chart from FEmusic_charts. Visitors get no song
// rows there, only chart playlists, so the first, the region's top chart,
// stands in; one page of it is enough.
func (c *Client) chart(ctx context.Context) ([]catalog.Song, error) {
	resp, err := c.call(ctx, "browse", map[string]any{"browseId": "FEmusic_charts"})
	if err != nil {
		return nil, fmt.Errorf("ytmusic: charts: %w", err)
	}
	songs, id := chartPage(resp)
	if len(songs) > 0 {
		return songs, nil
	}
	if id == "" {
		return nil, errors.New("ytmusic: charts: no chart")
	}
	resp, err = browse(ctx, c.call, "chart", id, map[string]any{"browseId": "VL" + id})
	if err != nil {
		return nil, err
	}
	_, rows, _ := playlistPage(id, resp)
	if songs = parseRows(rows, parseSong); len(songs) == 0 {
		return nil, fmt.Errorf("ytmusic: chart %q: no songs", id)
	}
	return songs, nil
}

// chartPage returns the charts page's ranked songs, if any, and its
// first playlist's ID. Chart carousels carry no identity but their
// contents, so any song rows count.
func chartPage(resp *response) ([]catalog.Song, string) {
	var rows []item
	var playlistID string
	for _, sec := range sectionList(resp).sections() {
		for _, sh := range [...]*shelf{sec.Shelf, sec.Carousel} {
			if sh == nil {
				continue
			}
			for _, v := range sh.Contents {
				rows = append(rows, v)
				if v.Card == nil {
					continue
				}
				id, _ := v.Card.NavigationEndpoint.Browse.target()
				if playlistID == "" && strings.HasPrefix(id, "VL") && len(id) > 2 {
					playlistID = id[2:]
				}
			}
		}
	}
	return ranked(rows), playlistID
}

func (c *Client) moods(ctx context.Context) ([]catalog.Mood, error) {
	resp, err := c.call(ctx, "browse", map[string]any{"browseId": "FEmusic_moods_and_genres"})
	if err != nil {
		return nil, fmt.Errorf("ytmusic: moods: %w", err)
	}
	moods := moodsPage(resp)
	if len(moods) == 0 {
		return nil, errors.New("ytmusic: moods: none parsed")
	}
	return moods, nil
}

func moodsPage(resp *response) []catalog.Mood {
	var moods []catalog.Mood
	seen := map[string]bool{}
	for _, sec := range sectionList(resp).sections() {
		if sec.Grid == nil {
			continue
		}
		for _, v := range sec.Grid.Items {
			button := v.MoodButton
			if button == nil || button.ClickCommand.Browse == nil {
				continue
			}
			mood := catalog.Mood{Name: button.ButtonText.Runs.String(), ID: button.ClickCommand.Browse.Params}
			if mood.Name == "" || mood.ID == "" || seen[mood.ID] {
				continue
			}
			seen[mood.ID] = true
			stripe := min(max(button.Solid.LeftStripeColor, 0), math.MaxUint32)
			mood.Tint = tint(uint32(stripe))
			moods = append(moods, mood)
		}
	}
	return moods
}
