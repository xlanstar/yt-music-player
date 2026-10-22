package ytmusic

import (
	"slices"
	"strconv"
	"strings"
	"yt-music-player/internal/catalog"
)

const (
	greyedOut    = "MUSIC_ITEM_RENDERER_DISPLAY_POLICY_GREY_OUT"
	deletedTitle = "Song deleted"
)

// seconds parses "m:ss" or "h:mm:ss"; anything else is 0.
func seconds(s string) int {
	s = strings.TrimSpace(s)
	// Counted rather than split: meta.read tries every subtitle run, and
	// most are not durations.
	if colons := strings.Count(s, ":"); colons < 1 || colons > 2 {
		return 0
	}
	total := 0
	for field := range strings.SplitSeq(s, ":") {
		n, err := strconv.Atoi(field)
		if err != nil || n < 0 || field == "" || field[0] == '+' {
			return 0
		}
		total = total*60 + n
	}
	return total
}

func year(s string) int {
	if len(s) != 4 {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1000 {
		return 0
	}
	return n
}

func release(s string) (catalog.Release, bool) {
	switch strings.TrimSpace(s) {
	case "Album":
		return catalog.ReleaseAlbum, true
	case "EP":
		return catalog.ReleaseEP, true
	case "Single":
		return catalog.ReleaseSingle, true
	}
	return "", false
}

// meta is what subtitle runs say about an item. Field order varies by
// page, so each run is recognized by its link or shape, not position.
type meta struct {
	artists, channels names
	album, albumID    string
	seconds, year     int
}

// names are joined as they are read: most items have one, which then
// costs no allocation.
type names struct {
	joined string
	n      int
}

func (l *names) add(name string) {
	if l.n == 0 {
		l.joined = name
	} else {
		l.joined += ", " + name
	}
	l.n++
}

// read adds runs to m. Unlinked text counts as an artist only where the
// caller knows pages put artists.
func (m *meta) read(rs runs, unlinkedArtists bool) {
	for i := range rs {
		r := &rs[i]
		runText := strings.TrimSpace(r.Text)
		if isSeparator(runText) {
			continue
		}
		if r.NavigationEndpoint != nil {
			m.readLink(runText, r.NavigationEndpoint.Browse)
			continue
		}
		if s := seconds(runText); s > 0 {
			m.seconds = s
			continue
		}
		if y := year(runText); y > 0 {
			m.year = y
			continue
		}
		if unlinkedArtists && !isCount(runText) {
			m.artists.add(runText)
		}
	}
}

func (m *meta) readLink(name string, ep *browseEndpoint) {
	id, page := ep.target()
	if id == "" {
		return
	}
	isAlbum := strings.HasPrefix(id, "MPRE") || page == "MUSIC_PAGE_TYPE_ALBUM" || page == "MUSIC_PAGE_TYPE_AUDIOBOOK"
	isArtist := page == "MUSIC_PAGE_TYPE_ARTIST" || page == "MUSIC_PAGE_TYPE_UNKNOWN" ||
		page == "" && strings.HasPrefix(id, "UC")
	switch {
	case isAlbum:
		m.album, m.albumID = name, id
	case isArtist:
		m.artists.add(name)
	case page == "MUSIC_PAGE_TYPE_USER_CHANNEL":
		m.channels.add(name)
	}
}

// artist names the performers; as on YouTube Music, a video's uploader
// stands in for a missing artist.
func (m *meta) artist() string {
	if m.artists.n > 0 {
		return m.artists.joined
	}
	return m.channels.joined
}

func isSeparator(s string) bool {
	switch s {
	case "", "•", "&", ",", "·":
		return true
	}
	return false
}

// isCount reports counts like "110M plays", which must not pass for an
// unlinked artist.
func isCount(s string) bool {
	if s == "" || s[0] < '0' || s[0] > '9' {
		return false
	}
	_, unit, ok := strings.Cut(s, " ")
	if !ok {
		return false
	}
	switch strings.ToLower(unit) {
	case "plays", "play", "views", "view", "songs", "song", "tracks", "track", "subscribers", "listeners":
		return true
	}
	return false
}

// videoID is the row's playable video, or "".
func (li *listItem) videoID() string {
	if id := li.Overlay.Renderer.Content.Play.Endpoint.Watch.VideoID; id != "" {
		return id
	}
	if col := li.column(0); len(col) > 0 && col[0].NavigationEndpoint != nil {
		if id := col[0].NavigationEndpoint.Watch.VideoID; id != "" {
			return id
		}
	}
	return li.PlaylistItemData.VideoID
}

// parseSong reads a song row of search results, albums and playlists.
// It skips rows that cannot play: without a video, or greyed out by
// YouTube (region locks, takedowns).
func parseSong(row item) (catalog.Song, bool) {
	li := row.ListItem
	if li == nil || li.DisplayPolicy == greyedOut {
		return catalog.Song{}, false
	}
	id := li.videoID()
	title := li.column(0).String()
	if id == "" || title == deletedTitle {
		return catalog.Song{}, false
	}
	var m meta
	for i := 1; i < len(li.FlexColumns); i++ {
		rs := li.column(i)
		if i == 1 {
			rs = skipLabel(rs, typeLabels)
		}
		m.read(rs, i == 1)
	}
	if m.seconds == 0 && len(li.FixedColumns) > 0 {
		fixed := &li.FixedColumns[0].Renderer.Text
		m.seconds = seconds(fixed.Runs.String() + fixed.SimpleText)
	}
	return catalog.Song{
		ID:      id,
		Title:   title,
		Artist:  m.artist(),
		Album:   m.album,
		AlbumID: m.albumID,
		Seconds: m.seconds,
		Art:     li.art(),
	}, true
}

// Labels that lead subtitles, matched in English like album kinds:
// unfiltered searches put "Song •" or "Video •" before the artists, and
// cards "Playlist •" before an unlinked owner.
var (
	typeLabels     = []string{"Song", "Video", "Episode"}
	playlistLabels = []string{"Playlist", "Chart", "Mix"}
)

// skipLabel drops a leading unlinked label, and the separator after it.
func skipLabel(rs runs, labels []string) runs {
	if len(rs) <= 2 || rs[0].NavigationEndpoint != nil || !slices.Contains(labels, rs[0].Text) {
		return rs
	}
	return rs[2:]
}

// parseAlbumRow reads an album row of search results.
func parseAlbumRow(row item) (catalog.Album, bool) {
	li := row.ListItem
	if li == nil {
		return catalog.Album{}, false
	}
	id, _ := li.NavigationEndpoint.Browse.target()
	if !strings.HasPrefix(id, "MPRE") {
		return catalog.Album{}, false
	}
	album := catalog.Album{ID: id, Title: li.column(0).String(), Art: li.art()}
	album.Kind, album.Artist, album.Year = subtitle(li.column(1))
	return album, true
}

// subtitle reads "Album • Daft Punk • 2001"; a missing kind is an album,
// which is what YouTube Music calls anything it does not label.
func subtitle(rs runs) (catalog.Release, string, int) {
	rel := catalog.ReleaseAlbum
	if len(rs) > 0 {
		if found, ok := release(rs[0].Text); ok {
			rel = found
			rs = rs[1:]
		}
	}
	var m meta
	m.read(rs, true)
	return rel, m.artist(), m.year
}
