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
	// covers are each slot's cover as laid out, the zero request where
	// none fits; those wholly on screen show.
	covers [numSlots]coverRequest
	status string
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
		// The header's cover spans its first lines, and is claimed first.
		if f.header.Dy() >= headerCoverRows {
			m.layCover(&f, slotHeader, f.header.Min.Y, headerCoverRows, p.header.art)
		}
	}
	st := m.mirror.State()
	if song, ok := m.mirror.Song(st.Current); ok && st.Current != "" {
		m.layCover(&f, slotBar, f.rects.bar.Min.Y+barSong, barCoverRows, song.Art)
	}
	if m.link.Connection > 0 {
		// Before any daemon spoke, its volume is no one's.
		f.status = m.status(st)
	}
	return f
}

// layCover lays out art's cover for slot s, rows tall from line y, where
// it fits (probe.request).
func (m *model) layCover(f *frame, s slot, y, rows int, art catalog.Art) {
	if r, ok := m.probe.request(s, y, rows, m.width, art); ok {
		f.covers[s] = r
	}
}

// followQueue keeps the queue's cursor on the current song while it
// follows it.
func (m *model) followQueue() {
	if m.queueFollows && len(m.mirror.Queue()) > 0 {
		m.queue.cursor = m.mirror.State().Index
	}
}
