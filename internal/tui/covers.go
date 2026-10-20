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

// coverFrame is what a frame's covers show, by slot, as covers.sync
// readied them: their lines from their first cell, blank until encoded,
// and the overlays' marks, a line's each, of the table that places them.
// It never changes: covers replace lines and marks, never write over them.
type coverFrame struct {
	lines [numSlots][]string
	marks [numSlots][]string
}

// appendLine appends the line of its cover sp holds, after its mark if
// an overlay's.
func (f *coverFrame) appendLine(b []byte, sp *span) []byte {
	if marks := f.marks[sp.slot]; sp.line < len(marks) {
		b = append(b, marks[sp.line]...)
	}
	if lines := f.lines[sp.slot]; sp.line < len(lines) {
		return append(b, lines[sp.line]...)
	}
	return appendSpaces(b, sp.cells)
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

// covers readies the covers a frame shows, on the UI thread: it loads
// and encodes them off the thread, keeps them by key, uploads Kitty's
// images and tells out where the overlays go. It makes no loader, no
// HTTP client, until a cover is wanted.
type covers struct {
	post   func(func())
	newArt func() *artwork.Loader
	art    *artwork.Loader

	cache [numCovers]cover
	frame uint64

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

	// tmux passes images through tmux to its terminal.
	tmux bool
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
func (cs *covers) beginFrame() {
	cs.frame++
	if cs.art != nil {
		cs.art.NextFrame()
	}
}

// coverKey is r's cover in caps' protocol, at r's size.
func (r coverRequest) key(caps termimage.Caps) coverKey {
	return coverKey{url: string(r.art), caps: caps, cols: r.rect.Dx(), rows: r.rect.Dy()}
}

// want readies r's cover, as shown this frame: it claims the cover's
// place, and encodes the image once loaded, again once a larger one came.
func (cs *covers) want(r coverRequest, caps termimage.Caps) {
	key := r.key(caps)
	i := cs.claim(key)
	c := &cs.cache[i]
	c.frame = cs.frame
	if cs.art == nil && cs.newArt != nil {
		cs.art = cs.newArt()
	}
	// Get may give another size while this one loads: a larger one, once
	// it came, encodes again.
	if px := max(caps.Pixels(key.cols, key.rows)); c.srcPx < px && !c.job.IsRunning() && cs.art != nil {
		if img := cs.art.Get(key.url, px); img != nil && img.RGBA.Rect.Dx() > c.srcPx {
			c.srcPx = img.RGBA.Rect.Dx()
			cs.encode(c, img.RGBA, cs.kittyBase+uint8(i)) //nolint:gosec // i < numCovers
		}
	}
}

// lines returns the lines of r's cover from cell 0: blank until encoded,
// or unless want readied it this frame; none without r.
func (cs *covers) lines(r coverRequest, caps termimage.Caps) []string {
	if r.art == "" {
		return nil
	}
	key := r.key(caps)
	for i := range cs.cache {
		if c := &cs.cache[i]; c.key == key && c.frame == cs.frame {
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
func (cs *covers) claim(key coverKey) int {
	oldest := 0
	for i := range cs.cache {
		c := &cs.cache[i]
		if c.key == key {
			return i
		}
		if c.frame < cs.cache[oldest].frame {
			oldest = i
		}
	}
	c := &cs.cache[oldest]
	c.job.Stop()
	*c = cover{key: key}
	c.Lines = blankLines(key.cols, key.rows)
	return oldest
}

// encode encodes img for c, at the size of c's key, off the UI thread.
// The job drops its result once c is claimed again or trimmed: c then
// holds another key, or none.
func (cs *covers) encode(c *cover, img *image.RGBA, id uint8) {
	key, tmux := c.key, cs.tmux
	job.Run(&c.job, cs.post, func(context.Context) (termimage.Encoded, error) {
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

// place makes the table of the overlays of reqs, which want readied,
// wholly on screen, of the generation after out's last if they changed.
func (cs *covers) place(reqs []coverRequest, caps termimage.Caps, screen image.Rectangle) {
	var places [numSlots]termimage.Placement
	for _, r := range reqs {
		if r.art == "" || !r.rect.In(screen) {
			continue
		}
		if c := cs.ready(r.key(caps)); c != nil && c.Overlay != "" {
			places[r.slot] = termimage.Placement{
				Seq: c.Overlay, X: r.rect.Min.X, Y: r.rect.Min.Y, Cols: r.rect.Dx(), Rows: r.rect.Dy(),
			}
		}
	}
	if places == cs.places {
		return
	}
	gen := cs.published + 1
	cs.table, cs.places = termimage.NewTable(gen, places[:]), places
	for s, p := range places {
		cs.marks[s] = cs.marks[s][:0]
		for r := range p.Rows {
			cs.marks[s] = append(cs.marks[s], termimage.Mark(gen, s, r))
		}
	}
}

// join joins a render's lines into the screen, each line of an overlay
// with its mark before the overlay's first cell.
func (cs *covers) join(lines []string) string {
	n := len(lines)
	for _, line := range lines {
		n += len(line)
	}
	for _, marks := range cs.marks {
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
		if mark, x := cs.markOf(y); mark != "" && len(line) > x {
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
func (cs *covers) markOf(y int) (mark string, x int) {
	for s, p := range cs.places {
		if p.Seq != "" && y >= p.Y && y < p.Y+p.Rows {
			return cs.marks[s][y-p.Y], p.X
		}
	}
	return "", 0
}

// commit sends, after a render, what the renderer cannot: Kitty's new
// uploads, and out the overlays' table if it changed. It runs once an
// Update at most, as out keeps two tables.
func (cs *covers) commit() tea.Cmd {
	if cs.table != nil && cs.table.Gen() > cs.published {
		cs.published = cs.table.Gen()
		if cs.out != nil {
			cs.out.publish(cs.table)
		}
	}
	cmd := cs.upload()
	cs.trim()
	return cmd
}

func (cs *covers) upload() tea.Cmd {
	var b strings.Builder
	for i := range cs.cache {
		if c := &cs.cache[i]; c.Upload != "" && c.frame == cs.frame {
			b.WriteString(c.Upload)
			c.Upload, cs.hasUploads = "", true
		}
	}
	if b.Len() == 0 {
		return nil
	}
	return tea.Raw(b.String())
}

// trim drops covers not shown, the oldest first, once their images take
// more than offScreenBytes; they encode again when shown.
func (cs *covers) trim() {
	total := 0
	for i := range cs.cache {
		total += cs.cache[i].size()
	}
	for total > offScreenBytes {
		oldest := -1
		for i := range cs.cache {
			c := &cs.cache[i]
			isDroppable := c.frame != cs.frame && c.size() > 0
			if isDroppable && (oldest < 0 || c.frame < cs.cache[oldest].frame) {
				oldest = i
			}
		}
		if oldest < 0 {
			return
		}
		c := &cs.cache[oldest]
		total -= c.size()
		c.job.Stop()
		*c = cover{}
	}
}

func (cs *covers) ready(key coverKey) *cover {
	for i := range cs.cache {
		if c := &cs.cache[i]; c.key == key && c.isReady {
			return c
		}
	}
	return nil
}

// isBusy reports covers loading or encoding, for tests to wait on.
func (cs *covers) isBusy() bool {
	if cs.art != nil && cs.art.Loading() {
		return true
	}
	for i := range cs.cache {
		if cs.cache[i].job.IsRunning() {
			return true
		}
	}
	return false
}

// close stops the loads and returns what frees the terminal's images.
func (cs *covers) close() string {
	if cs.art != nil {
		cs.art.Close()
	}
	var b strings.Builder
	for i := range cs.cache {
		cs.cache[i].job.Stop()
		if cs.hasUploads {
			// q=2: an id never sent is no error.
			b.WriteString(termimage.KittyDelete(cs.kittyBase+uint8(i), cs.tmux))
		}
	}
	return b.String()
}
