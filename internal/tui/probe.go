package tui

import (
	"image"
	"slices"
	"time"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/termimage"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
)

// probeWait is how long a terminal has to answer the probe.
const probeWait = time.Second

type probeTimeoutMsg struct{}

// start asks the terminal what it shows, unless YTM_IMAGES says or it
// shows none; then only the cell's pixels.
func (g *gfx) start() tea.Cmd {
	if !termimage.NeedsProbe(g.env) {
		g.isReady = true
		if g.detect(); g.caps.Protocol == termimage.None {
			return nil
		}
		return tea.Raw(termimage.CellSizeQuery)
	}
	return tea.Batch(
		tea.Raw(termimage.Probe(g.env.Tmux)),
		tea.Tick(probeWait, func(time.Time) tea.Msg { return probeTimeoutMsg{} }),
	)
}

// reply keeps an answer to the probe, or the color profile; it reports
// covers changing, false for other messages. Covers wait for the device
// attributes, which a terminal sends after its other answers, or the
// probe's time.
func (g *gfx) reply(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case uv.KittyGraphicsEvent:
		if msg.Options.ID != termimage.ProbeID {
			return false
		}
		g.replies.Kitty = string(msg.Payload) == "OK"
	case uv.CellSizeEvent:
		g.replies.CellW, g.replies.CellH = msg.Width, msg.Height
	case tea.TerminalVersionMsg:
		g.replies.Version = msg.Name
	case uv.PrimaryDeviceAttributesEvent:
		g.replies.Sixel = slices.Contains(msg, 4)
		g.isReady = true
	case probeTimeoutMsg:
		g.isReady = true
	case tea.ColorProfileMsg:
		g.env.Colors = msg.Profile >= colorprofile.ANSI256
	default:
		return false
	}
	return g.isReady && g.detect()
}

// detect picks the protocol and the cell's pixels, which size covers;
// it reports a change.
func (g *gfx) detect() bool {
	was := g.caps
	g.caps = termimage.Detect(g.env, g.replies)
	if g.caps.Protocol != termimage.None && g.art == nil && g.newArt != nil {
		g.art = g.newArt()
	}
	return g.caps != was
}

// resized asks the cell's pixels again after a resize: a font's size
// changes them.
func (g *gfx) resized() tea.Cmd {
	if !g.isReady || g.caps.Protocol == termimage.None {
		return nil
	}
	return tea.Raw(termimage.CellSizeQuery)
}

// coverCols returns the cells a square cover rows tall spans on a screen
// w wide; 0 where none shows: no protocol, or too narrow beside it.
func (g *gfx) coverCols(rows, w int) int {
	if g.caps.Protocol == termimage.None || rows <= 0 {
		return 0
	}
	if cols := g.caps.Fit(rows); w >= cols+coverMinWidth {
		return cols
	}
	return 0
}

// request returns art's cover for slot s, rows tall from line y's cell
// coverX, as wide as the terminal draws it square; ok is false where a
// screen w wide is too narrow for it, or art is no image.
func (g *gfx) request(s slot, y, rows, w int, art catalog.Art) (r coverRequest, ok bool) {
	cols := g.coverCols(rows, w)
	if cols == 0 || !art.IsURL() {
		return coverRequest{}, false
	}
	return coverRequest{rect: image.Rect(coverX, y, coverX+cols, y+rows), art: art, slot: s}, true
}
