package tui

import (
	"slices"
	"time"
	"yt-music-player/internal/termimage"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
)

// probeWait is how long a terminal has to answer the probe.
const probeWait = time.Second

type probeTimeoutMsg struct{}

// probe finds how the terminal shows images, on the UI thread: it asks
// the terminal unless YTM_IMAGES says, and keeps the caps that size and
// encode covers.
type probe struct {
	env     termimage.Env
	replies termimage.Replies
	caps    termimage.Caps
	isReady bool
}

// start asks the terminal what it shows, unless YTM_IMAGES says or it
// shows none; then only the cell's pixels.
func (p *probe) start() tea.Cmd {
	if !termimage.NeedsProbe(p.env) {
		p.isReady = true
		if p.detect(); p.caps.Protocol == termimage.None {
			return nil
		}
		return tea.Raw(termimage.CellSizeQuery)
	}
	return tea.Batch(
		tea.Raw(termimage.Probe(p.env.Tmux)),
		tea.Tick(probeWait, func(time.Time) tea.Msg { return probeTimeoutMsg{} }),
	)
}

// reply keeps an answer to the probe, or the color profile; it reports
// covers changing, false for other messages. Covers wait for the device
// attributes, which a terminal sends after its other answers, or the
// probe's time.
func (p *probe) reply(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case uv.KittyGraphicsEvent:
		if msg.Options.ID != termimage.ProbeID {
			return false
		}
		p.replies.Kitty = string(msg.Payload) == "OK"
	case uv.CellSizeEvent:
		p.replies.CellW, p.replies.CellH = msg.Width, msg.Height
	case tea.TerminalVersionMsg:
		p.replies.Version = msg.Name
	case uv.PrimaryDeviceAttributesEvent:
		p.replies.Sixel = slices.Contains(msg, 4)
		p.isReady = true
	case probeTimeoutMsg:
		p.isReady = true
	case tea.ColorProfileMsg:
		p.env.Colors = msg.Profile >= colorprofile.ANSI256
	default:
		return false
	}
	return p.isReady && p.detect()
}

// detect picks the protocol and the cell's pixels, which size covers;
// it reports a change.
func (p *probe) detect() bool {
	was := p.caps
	p.caps = termimage.Detect(p.env, p.replies)
	return p.caps != was
}

// resized asks the cell's pixels again after a resize: a font's size
// changes them.
func (p *probe) resized() tea.Cmd {
	if !p.isReady || p.caps.Protocol == termimage.None {
		return nil
	}
	return tea.Raw(termimage.CellSizeQuery)
}

// coverCols returns the cells a square cover rows tall spans on a screen
// w wide; 0 where none shows: no protocol, or too narrow beside it.
func (p *probe) coverCols(rows, w int) int {
	if p.caps.Protocol == termimage.None || rows <= 0 {
		return 0
	}
	if cols := p.caps.Fit(rows); w >= cols+coverMinWidth {
		return cols
	}
	return 0
}
