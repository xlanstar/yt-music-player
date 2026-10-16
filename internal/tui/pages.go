package tui

import (
	"strings"
	"yt-music-player/internal/catalog"
	"yt-music-player/locales"
)

// itemKind is what a list's row is.
type itemKind uint8

const (
	itemSong itemKind = iota + 1
	itemAlbum
	itemHeading
)

// item is a list's row; only the fields of its kind are set.
type item struct {
	kind  itemKind
	song  catalog.Song
	album catalog.Album
	// text is a heading's.
	text string
	// isCurrent marks the queue's current song.
	isCurrent bool
}

// page is what the body shows: header lines, then a message or a list.
type page struct {
	header []string

	// A message, when title is set; enter calls retry, if set.
	title, detail, hint string
	retry               func()

	// A list of n items.
	list *listState
	n    int
	at   func(i int) item
}

func (p *page) selectable(i int) bool { return p.at(i).kind != itemHeading }

// selected returns the item under the cursor, if the page lists any.
func (p *page) selected() (item, bool) {
	if p.title != "" || p.list == nil || p.n == 0 {
		return item{}, false
	}
	i := min(max(p.list.cursor, 0), p.n-1)
	return p.at(i), true
}

// page returns what the shown tab shows.
func (m *model) page() page {
	if m.tab == tabSearch {
		return m.searchPage()
	}
	return m.queuePage()
}

func (m *model) queuePage() page {
	queue := m.mirror.Queue()
	if len(queue) == 0 {
		return page{title: m.words.queueEmpty, detail: m.words.queueEmptyHint}
	}
	st := m.mirror.State()
	if m.queueFollows {
		m.queue.cursor = st.Index
	}
	return page{list: &m.queue, n: len(queue), at: func(i int) item {
		id := queue[i]
		song, ok := m.mirror.Song(id)
		if !ok {
			song = catalog.Song{ID: id, Title: id}
		}
		return item{kind: itemSong, song: song, isCurrent: i == st.Index && id == st.Current}
	}}
}

func (m *model) searchPage() page {
	p := page{header: []string{m.searchInput(), ""}}
	query := strings.TrimSpace(m.query)
	found := m.loader.Found()
	results := found.Results
	switch {
	case query == "":
		p.title, p.detail = m.words.searchTitle, m.words.searchHint
		return p
	case query == found.FailedQuery:
		p.title, p.detail = locales.SearchFailed(query), m.why(found.Err)
		p.hint = keyHint("enter", m.words.retry)
		p.retry = func() { m.loader.RetrySearch(query) }
		return p
	case query != found.Query:
		if results.IsEmpty() {
			p.title = locales.SearchingFor(query)
			return p
		}
		// The last results stay while new ones come, so the page does
		// not blink empty at every key.
		p.header[1] = styleMuted.Render(" " + m.words.searching)
	}
	if results.IsEmpty() {
		p.title, p.detail = locales.SearchNone(query), m.words.searchNoneHint
		return p
	}
	songs, albums := results.Songs, results.Albums
	// The headings' places: songs after 0, albums after albumsAt.
	albumsAt := -1
	n := 0
	if len(songs) > 0 {
		n += 1 + len(songs)
	}
	if len(albums) > 0 {
		albumsAt = n
		n += 1 + len(albums)
	}
	p.list, p.n = &m.search, n
	p.at = func(i int) item {
		switch {
		case i == albumsAt:
			return item{kind: itemHeading, text: m.words.albums}
		case albumsAt >= 0 && i > albumsAt:
			return item{kind: itemAlbum, album: albums[i-albumsAt-1]}
		case i == 0:
			return item{kind: itemHeading, text: m.words.songs}
		default:
			return item{kind: itemSong, song: songs[i-1]}
		}
	}
	return p
}

// searchInput is the search page's input line.
func (m *model) searchInput() string {
	prompt := styleKey.Render(" / ")
	switch {
	case m.isTyping:
		return prompt + m.query + styleAccent.Render("█")
	case m.query == "":
		return prompt + styleMuted.Render(m.words.searchPlaceholder)
	}
	return prompt + m.query
}
