package tui

import (
	"strings"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/remote"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Clicks are found from the layout render drew, worked out again on
// each click: clicks are rare, frames are not.
const (
	// doubleClick is how soon a second click on the same line makes a
	// double click.
	doubleClick = 400 * time.Millisecond
	// wheelRows is how far a turn of the wheel moves the cursor.
	wheelRows = 3
)

// The player bar's lines, from its top.
const (
	barProgress = 2
	barStatus   = 3
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

	bar := m.height - barLines
	switch body := m.bodyHeight(); {
	case ms.Y >= bar:
		return m.clickBar(ms.X, ms.Y-bar) || ok, nil
	case ms.Y == bar-1 && m.link.Health != remote.Connected:
		return m.bannerHasKey() && m.retryDaemon() || ok, nil
	case body < minBody:
		return ok, nil
	case ms.Y == 0:
		return m.clickTab(ms.X) || ok, nil
	case ms.Y >= headerLines && ms.Y < headerLines+body:
		acted, cmd := m.clickBody(ms.Y-headerLines, isDouble)
		return acted || ok, cmd
	}
	return ok, nil
}

// clickBar handles a click on line y of the player bar: the progress
// starts a drag, the help's key shows the help.
func (m *model) clickBar(x, y int) bool {
	switch y {
	case barProgress:
		return m.seekAt(x, true)
	case barStatus:
		right := width(keyHint("?", m.words.help) + " ")
		if m.notice == "" && x >= m.width-right && x < m.width-1 {
			m.isHelp = !m.isHelp
			return true
		}
	}
	return false
}

// bannerHasKey reports that the banner shows R's hint, which a click on
// it does.
func (m *model) bannerHasKey() bool {
	switch m.link.Health {
	case remote.Unresponsive, remote.Unavailable:
		return true
	case remote.DaemonOutdated:
		return !m.link.Daemon.IsCompatible
	}
	return false
}

// clickTab shows the tab at x of the header.
func (m *model) clickTab(x int) bool {
	at := tabsFrom(m.width)
	for i, label := range m.tabLabels() {
		cw := width(label) + 2
		if x >= at && x < at+cw {
			m.tab = tab(i)
			m.isHelp = false
			m.isTyping = m.isTyping && m.tab == tabSearch
			return true
		}
		at += cw + 1
	}
	return false
}

// clickBody handles a click on line y of the page: on the search input
// it types; on a list's row it selects, and plays or opens what was
// selected already or double-clicked; on a message's hint it does what
// the hint's key does.
func (m *model) clickBody(y int, isDouble bool) (ok bool, cmd tea.Cmd) {
	if m.isHelp {
		return false, nil
	}
	p := m.page()
	if y < len(p.header) {
		if y == 0 && m.tab == tabSearch && m.opened[tabSearch].kind == 0 && !m.isTyping {
			m.isTyping = true
			return true, nil
		}
		return false, nil
	}
	y -= len(p.header)
	if p.title != "" {
		if p.hint == "" || y != hintLine(&p, m.width) {
			return false, nil
		}
		switch {
		case p.retry != nil:
			p.retry()
			return true, nil
		case m.isSignedOut():
			return true, m.openDesktopCmd()
		}
		return false, nil
	}
	if p.list == nil {
		return false, nil
	}
	i := p.list.offset + y
	if i >= p.n || !p.selectable(i) {
		return false, nil
	}
	wasTyping := m.isTyping
	m.isTyping = false
	if p.list == &m.queue {
		m.queueFollows = false
	}
	isSelected := p.list.cursor == i
	p.list.cursor = i
	if wasTyping || !isSelected && !isDouble {
		return true, nil
	}
	m.lastClick.hasActed = true
	m.activate()
	return true, nil
}

// hintLine is the line of p's message, a page of w cells, that shows its
// hint, as appendMessage lays it out.
func hintLine(p *page, w int) int {
	y := 3 // a blank line, the title, a blank line
	if p.detail != "" {
		for range strings.Lines(lipgloss.Wrap(p.detail, max(w-2, 1), "")) {
			y++
		}
	}
	return y
}

// seekAt drags the progress to cell x of its line; a press must be on
// the bar, a drag goes on past its ends.
func (m *model) seekAt(x int, isPress bool) bool {
	st := m.mirror.State()
	if st.Current == "" || st.Duration <= 0 {
		return false
	}
	start, n := barSpan(clock(m.position(st)), clock(st.Duration), m.width)
	if n == 0 || isPress && (x < start || x >= start+n) {
		return false
	}
	frac := min(max((float64(x-start)+0.5)/float64(n), 0), 1)
	to := frac * st.Duration
	if !isPress && m.seek.to == to {
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
	return m.seekAt(x, false)
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
