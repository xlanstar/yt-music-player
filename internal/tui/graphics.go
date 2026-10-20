package tui

import (
	"context"
	"image"
	"math/rand/v2"
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
	// coverX is the cell covers show from, after a space.
	coverX = 1
	// coverMinWidth is the narrowest terminal that shows covers.
	coverMinWidth = 40
	// probeWait is how long a terminal has to answer the probe.
	probeWait = time.Second
	// numCovers bounds the covers kept: two show, the rest come back.
	numCovers = 8
	// offScreenBytes bounds the images kept for covers off screen:
	// overlays to draw again, Kitty's uploads not sent.
	offScreenBytes = 64 << 10
	// artMemory bounds the decoded pixels the TUI keeps besides those
	// shown: 4 covers of 64 pixels or 1 of 128. Never 0, artwork's 32 MB.
	artMemory  = 64 << 10
	artWorkers = 2
)

// slot is a place that shows a cover.
type slot uint8

const (
	slotBar slot = iota
	slotHeader
	numSlots
)

// coverKey is a cover in a protocol, at the size of coverRows cells.
type coverKey struct {
	url  string
	caps termimage.Caps
}

// placement is a cover a render placed on line y; its zero value is none.
type placement struct {
	key coverKey
	y   int
}

// cover is a cover encoded for the terminal, or being encoded; its
// Upload goes once sent.
type cover struct {
	termimage.Encoded
	key   coverKey
	frame uint64
	// srcPx is the width of the image encoded, or being encoded.
	srcPx   int
	isReady bool
	job     job.Job
}

// size is the bytes of c's images: an overlay, or an upload not sent.
func (c *cover) size() int { return len(c.Overlay) + len(c.Upload) }

type probeTimeoutMsg struct{}

// gfx shows covers, on the UI thread: it detects the protocol, loads
// and encodes covers off the thread, uploads Kitty's images and tells out
// where the overlays go. With no protocol it makes nothing: no loader, no
// HTTP client.
type gfx struct {
	env     termimage.Env
	replies termimage.Replies
	caps    termimage.Caps
	// cols is a cover's width, caps.Fit(coverRows).
	cols int
	// blank is a cover's lines while it loads.
	blank []string

	post   func(func())
	newArt func() *artwork.Loader
	art    *artwork.Loader

	covers [numCovers]cover
	frame  uint64
	placed [numSlots]placement

	// out draws the overlays of the frames that wrote their marks; nil
	// without a terminal.
	out *termOut
	// table is the overlays' last table, places what it holds and marks
	// its marks, by slot and line: a frame whose overlays did not change
	// keeps them, its lines the same bytes. published is out's last.
	table     *termimage.Table
	places    [numSlots]termimage.Placement
	marks     [numSlots][coverRows]string
	published uint64

	isReady bool
	// kittyBase is cover 0's Kitty image ID, a 256-color index (newKittyBase).
	kittyBase uint8
	// hasUploads keeps Kitty cleanup enabled even after uploaded covers are evicted.
	hasUploads bool
}

// newKittyBase picks a process's first Kitty image id: two ytm in one
// terminal, as in tmux's panes, would else replace and delete each
// other's images.
func newKittyBase() uint8 {
	return 1 + rand.N[uint8](256-numCovers)
}

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

// detect picks the protocol and sizes covers to the cell, and the blank
// they show while loading; it reports a change.
func (g *gfx) detect() bool {
	was := g.caps
	g.caps = termimage.Detect(g.env, g.replies)
	g.cols = g.caps.Fit(coverRows)
	g.blank = slices.Repeat([]string{strings.Repeat(" ", coverX+g.cols)}, coverRows)
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

// beginFrame starts a render's placements.
func (g *gfx) beginFrame() {
	g.frame++
	g.placed = [numSlots]placement{}
	if g.art != nil {
		g.art.NextFrame()
	}
}

// show places art's cover in slot s from line y of a screen w cells wide,
// and returns its lines, blank until it is ready, each after a space, and
// the cells they take; without a cover, empty lines and 0.
func (g *gfx) show(s slot, w, y int, art catalog.Art) (lines [coverRows]string, x int) {
	if g.caps.Protocol == termimage.None || w < g.cols+coverMinWidth || !art.IsURL() {
		return lines, 0
	}
	key := coverKey{url: string(art), caps: g.caps}
	i := g.claim(key)
	c := &g.covers[i]
	c.frame = g.frame
	g.placed[s] = placement{key: key, y: y}
	// Get may give another size while this one loads: a larger one, once
	// it came, encodes again.
	if px := max(g.caps.Pixels(g.cols, coverRows)); c.srcPx < px && !c.job.IsRunning() && g.art != nil {
		if img := g.art.Get(key.url, px); img != nil && img.RGBA.Rect.Dx() > c.srcPx {
			c.srcPx = img.RGBA.Rect.Dx()
			g.encode(c, img.RGBA, g.kittyBase+uint8(i)) //nolint:gosec // i < numCovers
		}
	}
	if c.isReady {
		return [coverRows]string(c.Lines), coverX + g.cols
	}
	return [coverRows]string(g.blank), coverX + g.cols
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
	key, cols, tmux := c.key, g.cols, g.env.Tmux
	job.Run(&c.job, g.post, func(context.Context) (termimage.Encoded, error) {
		e, err := termimage.Encode(img, key.caps, cols, coverRows, id, tmux)
		for i, line := range e.Lines {
			e.Lines[i] = " " + line
		}
		return e, err
	}, func(e termimage.Encoded, err error) {
		if err != nil {
			log.Debug().Err(err).Stringer("protocol", key.caps.Protocol).Msg("tui: cover not encoded")
			e.Lines = g.blank
		}
		c.Encoded, c.isReady = e, true
	})
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

// endFrame makes the table of the overlays the render placed, of the
// generation after out's last if they changed.
func (g *gfx) endFrame() {
	var places [numSlots]termimage.Placement
	for s, p := range g.placed {
		if c := g.ready(p.key); c != nil && c.Overlay != "" {
			places[s] = termimage.Placement{Seq: c.Overlay, X: coverX, Y: p.y, Cols: g.cols, Rows: coverRows}
		}
	}
	if places == g.places {
		return
	}
	gen := g.published + 1
	g.table, g.places = termimage.NewTable(gen, places[:]), places
	for s, p := range places {
		for r := range coverRows {
			g.marks[s][r] = ""
			if p.Seq != "" {
				g.marks[s][r] = termimage.Mark(gen, s, r)
			}
		}
	}
}

// join joins a render's lines into the screen, each line of an overlay
// with its mark before the overlay's first cell.
func (g *gfx) join(lines []string) string {
	n := len(lines)
	for _, line := range lines {
		n += len(line)
	}
	for s, p := range g.places {
		if p.Seq != "" {
			n += coverRows * len(g.marks[s][0])
		}
	}
	var b strings.Builder
	b.Grow(n)
	for y, line := range lines {
		if y > 0 {
			b.WriteByte('\n')
		}
		if mark := g.markOf(y); mark != "" && len(line) > coverX {
			// Covers start after coverX spaces, a byte each.
			b.WriteString(line[:coverX])
			b.WriteString(mark)
			line = line[coverX:]
		}
		b.WriteString(line)
	}
	return b.String()
}

// markOf returns the mark of line y, empty off the overlays.
func (g *gfx) markOf(y int) string {
	for s, p := range g.places {
		if p.Seq != "" && y >= p.Y && y < p.Y+p.Rows {
			return g.marks[s][y-p.Y]
		}
	}
	return ""
}

// commit sends, after a render, what the renderer cannot: Kitty's new
// uploads, and out the overlays' table if it changed. It runs once an
// Update at most, as out keeps two tables.
func (g *gfx) commit() tea.Cmd {
	if g.table != nil && g.table.Gen() > g.published {
		g.published = g.table.Gen()
		if g.out != nil {
			g.out.publish(g.table)
		}
	}
	cmd := g.upload()
	g.trim()
	return cmd
}

func (g *gfx) upload() tea.Cmd {
	var b strings.Builder
	for i := range g.covers {
		if c := &g.covers[i]; c.Upload != "" && c.frame == g.frame {
			b.WriteString(c.Upload)
			c.Upload, g.hasUploads = "", true
		}
	}
	if b.Len() == 0 {
		return nil
	}
	return tea.Raw(b.String())
}

// trim drops covers not shown, the oldest first, once their images take
// more than offScreenBytes; they encode again when shown.
func (g *gfx) trim() {
	total := 0
	for i := range g.covers {
		total += g.covers[i].size()
	}
	for total > offScreenBytes {
		oldest := -1
		for i := range g.covers {
			c := &g.covers[i]
			isDroppable := c.frame != g.frame && c.size() > 0
			if isDroppable && (oldest < 0 || c.frame < g.covers[oldest].frame) {
				oldest = i
			}
		}
		if oldest < 0 {
			return
		}
		c := &g.covers[oldest]
		total -= c.size()
		c.job.Stop()
		*c = cover{}
	}
}

func (g *gfx) ready(key coverKey) *cover {
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
		g.covers[i].job.Stop()
		if g.hasUploads {
			// q=2: an id never sent is no error.
			b.WriteString(termimage.KittyDelete(g.kittyBase+uint8(i), g.env.Tmux))
		}
	}
	return b.String()
}
