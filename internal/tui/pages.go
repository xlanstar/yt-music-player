package tui

import (
	"yt-music-player/internal/catalog"
)

// itemKind is what a list's row is.
type itemKind uint8

const (
	itemSong itemKind = iota + 1
	itemHeading
)

// item is a list's row; only the fields of its kind are set.
type item struct {
	kind itemKind
	song catalog.Song
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
