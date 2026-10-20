package tui

import (
	"image"
	"yt-music-player/internal/catalog"
)

// frame is what one render draws, prepared before drawing: the layout,
// the page with its own layout, the covers that show and the bar's
// status. It lives on the UI thread for one settle; keys and clicks read
// the model afresh, never a frame.
type frame struct {
	rects frameRects
	// page is the body's, unless the help shows or the body has no rows.
	page page
	// header and content split the body: the page's header, and the rows
	// of its message or list.
	header, content image.Rectangle
	// bar and head are the bar's and the page header's covers and the
	// text beside them, in their own cells.
	bar, head beside
	status    string
}

// prepare makes the frame that draw draws, and changes beforehand what
// drawing would: the queue's cursor following the current song, the
// list scrolled to the rows that show, the memos.
func (m *model) prepare() frame {
	m.followQueue()
	f := frame{rects: layout(m.layoutIn())}
	w := m.width
	if w <= 0 || m.height <= 0 {
		return f
	}
	if body := f.rects.body; body.Dy() > 0 && !m.isHelp {
		f.page = m.page()
		p := &f.page
		f.header, f.content = splitBody(body, p.header.rows())
		if p.title == "" && p.list != nil {
			p.list.fit(p.n, f.content.Dy(), p.selectable)
		}
		// The header's cover spans its first lines.
		cols := 0
		if f.header.Dy() >= headerCoverRows {
			cols = m.coverCols(headerCoverRows, p.header.art)
		}
		f.head = layBeside(0, cols, headerCoverRows)
	}
	st := m.mirror.State()
	cols := 0
	if song, ok := m.mirror.Song(st.Current); ok && st.Current != "" {
		cols = m.coverCols(barCoverRows, song.Art)
	}
	f.bar = layBeside(barSong, cols, barCoverRows)
	if m.link.Connection > 0 {
		// Before any daemon spoke, its volume is no one's.
		f.status = m.status(st)
	}
	return f
}

// coverCols returns the cells art's cover spans, rows tall: none unless
// it is an image the terminal shows beside the rest (probe.coverCols).
func (m *model) coverCols(rows int, art catalog.Art) int {
	if !art.IsURL() {
		return 0
	}
	return m.probe.coverCols(rows, m.width)
}

// followQueue keeps the queue's cursor on the current song while it
// follows it.
func (m *model) followQueue() {
	if m.queueFollows && len(m.mirror.Queue()) > 0 {
		m.queue.cursor = m.mirror.State().Index
	}
}
