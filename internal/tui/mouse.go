package tui

import (
	"image"
	"slices"
	"time"
	"yt-music-player/internal/api"

	tea "charm.land/bubbletea/v2"
)

const (
	// doubleClick is how soon a second click on the same line makes a
	// double click.
	doubleClick = 400 * time.Millisecond
	// wheelRows is how far a turn of the wheel moves the cursor.
	wheelRows = 3
)

// click is a left click, as the next one reads it.
type click struct {
	at time.Time
	y  int
	// hasActed: it played or opened what it clicked, so that a third
	// click of a triple click does not do so again.
	hasActed bool
}

// seeking is the progress as the pointer drags it: its position shows
// instead of the Mirror's, and is sent once let go. After, it still
// shows until a State answers, as the Desktop's sliders do (held).
type seeking struct {
	song               string
	to                 float64
	isDragging, isHeld bool
	sentGen            uint64
}

// position is where the current song shows: the Mirror's, unless
// dragged or not yet answered.
func (m *model) position(st api.State) float64 {
	s := &m.seek
	isShown := s.isDragging || s.isHeld && m.mirror.StateGen() == s.sentGen
	if isShown && st.Current == s.song {
		return s.to
	}
	return m.mirror.Position(m.now())
}

// hit is what a click acts on, drawn over rect: whole as laid out, which
// a drag's ratio reads, and shown, its part on screen that clicks hit.
type hit struct {
	rect, shown image.Rectangle
	kind        hitKind
	tab         tab
	// i is the row's item in list.
	i    int
	list *listState
}

type hitKind uint8

const (
	hitTab hitKind = iota + 1
	hitInput
	hitRow
	hitHint
	hitBanner
	hitProgress
	hitHelp
)

// mark notes h over cells [x0, x1) of line y, as draw appends lines;
// drawLines puts it where the line goes.
func (m *model) mark(y, x0, x1 int, h hit) {
	h.rect = image.Rect(x0, y, x1, y+1)
	m.scene.hits = append(m.scene.hits, h)
}

// dropHits drops the hits marked on lines from line from, as draw cuts
// them.
func (m *model) dropHits(from int) {
	m.scene.hits = slices.DeleteFunc(m.scene.hits, func(h hit) bool { return h.rect.Min.Y >= from })
}

// hitAt is the hit drawn at (x, y).
func (m *model) hitAt(x, y int) (hit, bool) {
	hits := m.scene.hits
	for i := range hits {
		if image.Pt(x, y).In(hits[i].shown) {
			return hits[i], true
		}
	}
	return hit{}, false
}

// click handles a left click; ok is false for one that does nothing.
func (m *model) click(ms tea.Mouse) (ok bool, cmd tea.Cmd) {
	if ms.Button != tea.MouseLeft || m.width <= 0 || m.height <= 0 {
		return false, nil
	}
	// A release the terminal never told ends here.
	ok = m.release()
	now := m.now()
	prev := m.lastClick
	isDouble := prev.y == ms.Y && !prev.at.IsZero() && now.Sub(prev.at) < doubleClick
	m.lastClick = click{at: now, y: ms.Y}
	if isDouble && prev.hasActed {
		return ok, nil
	}
	h, found := m.hitAt(ms.X, ms.Y)
	if !found {
		return ok, nil
	}
	switch h.kind {
	case hitTab:
		m.tab = h.tab
		m.isHelp = false
		m.isTyping = m.isTyping && m.tab == tabSearch
	case hitInput:
		m.isTyping = true
	case hitRow:
		m.clickRow(h, isDouble)
	case hitHint:
		switch p := m.page(); {
		case p.retry != nil:
			p.retry()
		case m.isSignedOut():
			return true, m.openDesktopCmd()
		default:
			return ok, nil
		}
	case hitBanner:
		return m.retryDaemon() || ok, nil
	case hitProgress:
		return m.seekAt(ms.X, h) || ok, nil
	case hitHelp:
		m.isHelp = !m.isHelp
	}
	return true, nil
}

// clickRow selects row h, and plays or opens it if it was selected
// already or double-clicked. A row drawn before the page changed, no
// longer on its list, plays nothing; the next fit moves its selection.
func (m *model) clickRow(h hit, isDouble bool) {
	wasTyping := m.isTyping
	m.isTyping = false
	if h.list == &m.queue {
		m.queueFollows = false
	}
	isSelected := h.list.cursor == h.i
	h.list.cursor = h.i
	if wasTyping || !isSelected && !isDouble {
		return
	}
	p := m.page()
	if p.list != h.list || p.title != "" || h.i < 0 || h.i >= p.n || !p.selectable(h.i) {
		return
	}
	m.lastClick.hasActed = true
	m.activateOn(&p)
}

// seekAt drags the progress, drawn at bar, to cell x; a drag goes on
// past its ends.
func (m *model) seekAt(x int, bar hit) bool {
	st := m.mirror.State()
	frac := min(max((float64(x-bar.rect.Min.X)+0.5)/float64(bar.rect.Dx()), 0), 1)
	to := frac * st.Duration
	if m.seek.isDragging && m.seek.to == to {
		return false
	}
	m.seek = seeking{song: st.Current, to: to, isDragging: true}
	return true
}

// drag follows the pointer while the progress is dragged.
func (m *model) drag(x int) bool {
	if !m.seek.isDragging {
		return false
	}
	hits := m.scene.hits
	for i := range hits {
		if hits[i].kind == hitProgress {
			return m.seekAt(x, hits[i])
		}
	}
	return false
}

// release lets the progress go: the song seeks to where it was dragged,
// once, if it still plays.
func (m *model) release() bool {
	s := &m.seek
	if !s.isDragging {
		return false
	}
	s.isDragging = false
	st := m.mirror.State()
	if st.Current != s.song {
		return true
	}
	m.ctl.Seek(s.to)
	s.isHeld, s.sentGen = true, m.mirror.StateGen()
	return true
}

// wheel moves the shown list's cursor a few rows.
func (m *model) wheel(b tea.MouseButton) bool {
	if m.isHelp {
		return false
	}
	switch b {
	case tea.MouseWheelUp:
		return m.scroll(-wheelRows)
	case tea.MouseWheelDown:
		return m.scroll(wheelRows)
	}
	return false
}

// scroll moves the shown list's cursor by delta, reporting a move.
func (m *model) scroll(delta int) bool {
	p := m.page()
	if p.list == nil || p.n == 0 || p.title != "" {
		return false
	}
	before := p.list.cursor
	m.moveBy(delta)
	return p.list.cursor != before
}
