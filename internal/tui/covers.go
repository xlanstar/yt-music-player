package tui

import (
	"context"
	"image"
	"math/rand/v2"
	"slices"
	"strings"
	"yt-music-player/internal/artwork"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/job"
	"yt-music-player/internal/termimage"

	tea "charm.land/bubbletea/v2"
	"github.com/rs/zerolog/log"
)

const (
	// coverX is the cell covers show from: their lines start with as many
	// spaces, until draw places covers by their cells.
	coverX = 1
	// coverMinWidth is the narrowest terminal that shows covers.
	coverMinWidth = 40
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

// coverRequest is a cover a part of the screen shows: art's image over
// rect's cells, in slot. Its zero value is none.
type coverRequest struct {
	rect image.Rectangle
	art  catalog.Art
	slot slot
}

// coverKey is a cover as encoded: an image in a protocol over cols×rows
// cells. The same image at two sizes is two covers.
type coverKey struct {
	url        string
	caps       termimage.Caps
	cols, rows int
}

// cover is a cover encoded for the terminal, or being encoded, its Lines
// blank meanwhile; its Upload goes once sent.
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

// gfx shows covers, on the UI thread: it detects the protocol, loads
// and encodes covers off the thread, uploads Kitty's images and tells out
// where the overlays go. With no protocol it makes nothing: no loader, no
// HTTP client.
type gfx struct {
	env     termimage.Env
	replies termimage.Replies
	caps    termimage.Caps

	post   func(func())
	newArt func() *artwork.Loader
	art    *artwork.Loader

	covers [numCovers]cover
	frame  uint64

	// out draws the overlays of the frames that wrote their marks; nil
	// without a terminal.
	out *termOut
	// table is the overlays' last table, places what it holds and marks
	// its marks, by slot and line: a frame whose overlays did not change
	// keeps them, its lines the same bytes. published is out's last.
	table     *termimage.Table
	places    [numSlots]termimage.Placement
	marks     [numSlots][]string
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

// beginFrame starts a frame's covers, before want.
func (g *gfx) beginFrame() {
	g.frame++
	if g.art != nil {
		g.art.NextFrame()
	}
}

// key is r's cover in the terminal's protocol, at r's size.
func (g *gfx) key(r coverRequest) coverKey {
	return coverKey{url: string(r.art), caps: g.caps, cols: r.rect.Dx(), rows: r.rect.Dy()}
}

// want readies r's cover, as shown this frame: it claims the cover's
// place, and encodes the image once loaded, again once a larger one came.
func (g *gfx) want(r coverRequest) {
	key := g.key(r)
	i := g.claim(key)
	c := &g.covers[i]
	c.frame = g.frame
	// Get may give another size while this one loads: a larger one, once
	// it came, encodes again.
	if px := max(g.caps.Pixels(key.cols, key.rows)); c.srcPx < px && !c.job.IsRunning() && g.art != nil {
		if img := g.art.Get(key.url, px); img != nil && img.RGBA.Rect.Dx() > c.srcPx {
			c.srcPx = img.RGBA.Rect.Dx()
			g.encode(c, img.RGBA, g.kittyBase+uint8(i)) //nolint:gosec // i < numCovers
		}
	}
}

// lines returns the lines of r's cover from cell 0: blank until encoded,
// or unless want readied it this frame; none without r.
func (g *gfx) lines(r coverRequest) []string {
	if r.art == "" {
		return nil
	}
	key := g.key(r)
	for i := range g.covers {
		if c := &g.covers[i]; c.key == key && c.frame == g.frame {
			return c.Lines
		}
	}
	return blankLines(key.cols, key.rows)
}

// blankLines are the lines of a cover of cols×rows cells while it loads.
func blankLines(cols, rows int) []string {
	return slices.Repeat([]string{strings.Repeat(" ", coverX+cols)}, rows)
}

// claim returns key's cover, or the one shown longest ago, taken for it.
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
	c.Lines = blankLines(key.cols, key.rows)
	return oldest
}

// encode encodes img for c, at the size of c's key, off the UI thread.
// The job drops its result once c is claimed again or trimmed: c then
// holds another key, or none.
func (g *gfx) encode(c *cover, img *image.RGBA, id uint8) {
	key, tmux := c.key, g.env.Tmux
	job.Run(&c.job, g.post, func(context.Context) (termimage.Encoded, error) {
		e, err := termimage.Encode(img, key.caps, key.cols, key.rows, id, tmux)
		indent := strings.Repeat(" ", coverX)
		for i, line := range e.Lines {
			e.Lines[i] = indent + line
		}
		return e, err
	}, func(e termimage.Encoded, err error) {
		if err != nil {
			log.Debug().Err(err).Stringer("protocol", key.caps.Protocol).Msg("tui: cover not encoded")
			e.Lines = blankLines(key.cols, key.rows)
		}
		c.Encoded, c.isReady = e, true
	})
}

// place makes the table of the overlays of covers, which want readied,
// wholly on screen, of the generation after out's last if they changed.
func (g *gfx) place(covers []coverRequest, screen image.Rectangle) {
	var places [numSlots]termimage.Placement
	for _, r := range covers {
		if r.art == "" || !r.rect.In(screen) {
			continue
		}
		if c := g.ready(g.key(r)); c != nil && c.Overlay != "" {
			places[r.slot] = termimage.Placement{
				Seq: c.Overlay, X: r.rect.Min.X, Y: r.rect.Min.Y, Cols: r.rect.Dx(), Rows: r.rect.Dy(),
			}
		}
	}
	if places == g.places {
		return
	}
	gen := g.published + 1
	g.table, g.places = termimage.NewTable(gen, places[:]), places
	for s, p := range places {
		g.marks[s] = g.marks[s][:0]
		for r := range p.Rows {
			g.marks[s] = append(g.marks[s], termimage.Mark(gen, s, r))
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
	for _, marks := range g.marks {
		for _, mark := range marks {
			n += len(mark)
		}
	}
	var b strings.Builder
	b.Grow(n)
	for y, line := range lines {
		if y > 0 {
			b.WriteByte('\n')
		}
		if mark, x := g.markOf(y); mark != "" && len(line) > x {
			// The cells before a cover are spaces, a byte each.
			b.WriteString(line[:x])
			b.WriteString(mark)
			line = line[x:]
		}
		b.WriteString(line)
	}
	return b.String()
}

// markOf returns the mark of line y and the cell it goes before; empty
// off the overlays.
func (g *gfx) markOf(y int) (mark string, x int) {
	for s, p := range g.places {
		if p.Seq != "" && y >= p.Y && y < p.Y+p.Rows {
			return g.marks[s][y-p.Y], p.X
		}
	}
	return "", 0
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
