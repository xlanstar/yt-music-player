package tui

import (
	"context"
	"image"
	"slices"
	"strings"
	"time"
	"yt-music-player/internal/artwork"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/job"
	"yt-music-player/internal/termimage"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/rs/zerolog/log"
)

const (
	// coverRows is a cover's height: the player bar's song and progress,
	// a collection's title and subtitle.
	coverRows = 2
	// coverMinWidth is the narrowest terminal that shows covers.
	coverMinWidth = 40
	// maxCoverPixels bounds what Kitty and iTerm2 get, which they scale.
	maxCoverPixels = 256
	// probeWait is how long a terminal has to answer the probe.
	probeWait = time.Second
	// overlayDelay lets the frame that blanked an overlay's cells out
	// before the image: two of the renderer's frames, and one more.
	overlayDelay = 3 * time.Second / fps
	// numCovers bounds the covers kept: two show, the rest come back.
	numCovers = 8
	// kittyBase is cover 0's Kitty image id, a 256-color index.
	kittyBase = 176
	// overlayBytes bounds the overlay images kept off screen.
	overlayBytes = 256 << 10
	// artMemory bounds the decoded pixels the TUI keeps: 16 covers of
	// 64 pixels or 4 of 128.
	artMemory  = 256 << 10
	artWorkers = 2
)

// slot is a place that shows a cover.
type slot uint8

const (
	slotBar slot = iota
	slotHeader
	numSlots
)

// coverKey is a cover at a size, in a protocol, whose pixels w×h are.
type coverKey struct {
	url        string
	p          termimage.Protocol
	cols, rows int
	w, h       int
}

// placement is a cover a render placed at cell x, y; its zero value is none.
type placement struct {
	key  coverKey
	x, y int
}

// cover is a cover encoded for the terminal, or being encoded.
type cover struct {
	key coverKey
	// lines are what its cells show: blocks, Kitty's placeholders, or an
	// overlay's blank.
	lines []string
	// seq is an overlay's image, kept to draw again, or Kitty's upload,
	// dropped once sent.
	seq             string
	frame           uint64
	isReady, isSent bool
	job             job.Job
}

// encoded is what an encoding job returns.
type encoded struct {
	lines []string
	seq   string
}

type probeTimeoutMsg struct{}

// overlayMsg draws the overlays of commit gen, if still current.
type overlayMsg struct{ gen uint64 }

// gfx shows covers, on the UI thread: it detects the protocol, loads
// and encodes covers off the thread, and sends what the renderer cannot.
// With no protocol it makes nothing: no loader, no HTTP client.
type gfx struct {
	env     termimage.Env
	replies termimage.Replies
	caps    termimage.Caps
	// cols is a cover's width, caps.Fit(coverRows).
	cols    int
	isReady bool

	post   func(func())
	newArt func() *artwork.Loader
	art    *artwork.Loader

	covers [numCovers]cover
	frame  uint64
	placed [numSlots]placement
	// drawn are the overlays the terminal shows, want those it should:
	// while they differ, an overlayMsg of gen will draw them.
	drawn, want [numSlots]placement
	gen         uint64
	blank       []string
}

// start asks the terminal what it shows, unless YTM_IMAGES says; then
// only the cell's pixels.
func (g *gfx) start() tea.Cmd {
	if _, ok := termimage.ParseOverride(g.env.Override); ok {
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

// detect picks the protocol and sizes covers to the cell, and the blank
// they show while loading; it reports a change.
func (g *gfx) detect() bool {
	was := g.caps
	g.caps = termimage.Detect(g.env, g.replies)
	g.cols = g.caps.Fit(coverRows)
	g.blank = slices.Repeat([]string{strings.Repeat(" ", g.cols)}, coverRows)
	if g.caps.Protocol != termimage.None && g.art == nil && g.newArt != nil {
		g.art = g.newArt()
	}
	return g.caps != was
}

// resized follows a resize, which repaints every cell: overlays again,
// and the cell's pixels, which a font's size changes.
func (g *gfx) resized() tea.Cmd {
	if !g.isReady || g.caps.Protocol == termimage.None {
		return nil
	}
	g.drawn, g.want = [numSlots]placement{}, [numSlots]placement{}
	return tea.Raw(termimage.CellSizeQuery)
}

// isOn reports covers showing in w cells.
func (g *gfx) isOn(w int) bool {
	return g.caps.Protocol != termimage.None && w >= g.cols+coverMinWidth
}

// beginFrame starts a render's placements.
func (g *gfx) beginFrame() {
	g.frame++
	g.placed = [numSlots]placement{}
	if g.art != nil {
		g.art.NextFrame()
	}
}

// place puts art's cover at cell x, y and returns its lines, blank
// until it is ready; nil shows none (no protocol, no image).
func (g *gfx) place(s slot, x, y int, art catalog.Art) []string {
	if g.caps.Protocol == termimage.None || !isURL(art) {
		return nil
	}
	w, h := g.caps.Pixels(g.cols, coverRows, maxCoverPixels)
	key := coverKey{url: string(art), p: g.caps.Protocol, cols: g.cols, rows: coverRows, w: w, h: h}
	i := g.claim(key)
	c := &g.covers[i]
	c.frame = g.frame
	g.placed[s] = placement{key: key, x: x, y: y}
	if c.isReady {
		return c.lines
	}
	if !c.job.IsRunning() && g.art != nil {
		if img := g.art.Get(key.url, max(w, h)); img != nil {
			g.encode(c, img.RGBA, kittyBase+uint8(i)) //nolint:gosec // i < numCovers
		}
	}
	return g.blank
}

// claim returns key's cover, or the one placed longest ago, taken for it.
func (g *gfx) claim(key coverKey) int {
	oldest := 0
	for i := range g.covers {
		c := &g.covers[i]
		if c.key == key {
			return i
		}
		if c.frame < g.covers[oldest].frame {
			oldest = i
		}
	}
	c := &g.covers[oldest]
	c.job.Stop()
	*c = cover{key: key}
	return oldest
}

// encode encodes img for c off the UI thread.
func (g *gfx) encode(c *cover, img *image.RGBA, id uint8) {
	key, tmux := c.key, g.caps.Tmux
	job.Run(&c.job, g.post, func(context.Context) (encoded, error) {
		return encodeCover(img, key, id, tmux)
	}, func(e encoded, err error) {
		if err != nil {
			log.Debug().Err(err).Stringer("protocol", key.p).Msg("tui: cover not encoded")
			e.lines = g.blank
		}
		c.lines, c.seq, c.isReady = e.lines, e.seq, true
	})
}

func encodeCover(src *image.RGBA, k coverKey, id uint8, tmux bool) (encoded, error) {
	img := termimage.Fill(src, k.w, k.h)
	switch k.p {
	case termimage.Blocks:
		return encoded{lines: termimage.BlockLines(img)}, nil
	case termimage.Kitty:
		return encoded{
			lines: termimage.KittyCells(id, k.cols, k.rows),
			seq:   termimage.KittyUpload(img, id, k.cols, k.rows, tmux),
		}, nil
	case termimage.Sixel:
		return encoded{lines: termimage.OverlayCells(k.cols, k.rows), seq: termimage.SixelImage(img)}, nil
	case termimage.ITerm2:
		seq, err := termimage.ITerm2Image(img, k.cols, k.rows, tmux)
		return encoded{lines: termimage.OverlayCells(k.cols, k.rows), seq: seq}, err
	}
	return encoded{}, nil
}

// clip moves the placements up by cut lines, as render cuts its top,
// and drops those cut.
func (g *gfx) clip(cut int) {
	for s := range g.placed {
		p := &g.placed[s]
		p.y -= cut
		if p.y < 0 {
			*p = placement{}
		}
	}
}

// commit sends, after a render, what the renderer cannot: Kitty's new
// uploads at once, overlays a few frames later.
func (g *gfx) commit() tea.Cmd {
	switch p := g.caps.Protocol; {
	case p == termimage.Kitty:
		return g.upload()
	case p.IsOverlay():
		return g.scheduleOverlays()
	}
	return nil
}

func (g *gfx) upload() tea.Cmd {
	var cmds []tea.Cmd
	for i := range g.covers {
		c := &g.covers[i]
		if c.isReady && !c.isSent && c.seq != "" && c.frame == g.frame {
			cmds = append(cmds, tea.Raw(c.seq))
			c.seq, c.isSent = "", true
		}
	}
	return tea.Batch(cmds...)
}

func (g *gfx) scheduleOverlays() tea.Cmd {
	var cur [numSlots]placement
	for s, p := range g.placed {
		if c := g.ready(p.key); c != nil && c.seq != "" {
			cur[s] = p
		}
	}
	g.trimOverlays()
	for s := range cur {
		if cur[s] != g.drawn[s] {
			// Its cells repaint, wiping the image.
			g.drawn[s] = placement{}
		}
	}
	if cur == g.want {
		// Drawn, or scheduled to be.
		return nil
	}
	g.want = cur
	if cur == g.drawn {
		return nil
	}
	g.gen++
	gen := g.gen
	return tea.Tick(overlayDelay, func(time.Time) tea.Msg { return overlayMsg{gen} })
}

// overlay draws the overlays commit scheduled, unless the screen changed
// since; the renderer's frame blanked their cells meanwhile.
func (g *gfx) overlay(msg overlayMsg) tea.Cmd {
	if msg.gen != g.gen {
		return nil
	}
	var b strings.Builder
	for s, p := range g.want {
		if c := g.ready(p.key); c != nil && p != g.drawn[s] {
			b.WriteString(termimage.Overlay(c.seq, p.x, p.y))
		}
	}
	g.drawn = g.want
	if b.Len() == 0 {
		return nil
	}
	return tea.Raw(b.String())
}

// trimOverlays drops the images of covers not shown once they take more
// than overlayBytes; they encode again when shown.
func (g *gfx) trimOverlays() {
	total := 0
	for i := range g.covers {
		total += len(g.covers[i].seq)
	}
	for i := range g.covers {
		c := &g.covers[i]
		if total <= overlayBytes {
			return
		}
		if c.frame != g.frame && c.seq != "" {
			total -= len(c.seq)
			*c = cover{}
		}
	}
}

func (g *gfx) ready(key coverKey) *cover {
	if key == (coverKey{}) {
		return nil
	}
	for i := range g.covers {
		if c := &g.covers[i]; c.key == key && c.isReady {
			return c
		}
	}
	return nil
}

// isBusy reports covers loading or encoding, for tests to wait on.
func (g *gfx) isBusy() bool {
	if g.art != nil && g.art.Loading() {
		return true
	}
	for i := range g.covers {
		if g.covers[i].job.IsRunning() {
			return true
		}
	}
	return false
}

// close stops the loads and returns what frees the terminal's images.
func (g *gfx) close() string {
	if g.art != nil {
		g.art.Close()
	}
	var b strings.Builder
	for i := range g.covers {
		c := &g.covers[i]
		c.job.Stop()
		if c.isSent {
			b.WriteString(termimage.KittyDelete(kittyBase+uint8(i), g.caps.Tmux))
		}
	}
	return b.String()
}

// isURL tells a cover to fetch from a generated one, as the placeholder's.
func isURL(art catalog.Art) bool {
	return strings.HasPrefix(string(art), "https://") || strings.HasPrefix(string(art), "http://")
}
