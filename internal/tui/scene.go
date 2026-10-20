package tui

import (
	"fmt"
	"image"
	"slices"
	"yt-music-player/internal/catalog"

	"github.com/rs/zerolog/log"
)

// panicOnOverlap makes drawing over cells already drawn a panic, as tests
// set (TestMain); a release drops what came later instead.
var panicOnOverlap bool

// span is what a row shows from cell x, cells wide: text in its paint,
// or line of slot's cover, whose cells it holds for covers.sync's lines.
type span struct {
	x, cells int
	text     string
	paint    paint
	line     int
	slot     slot
	isCover  bool
}

// scene is a frame's screen as regions draw it: rows of spans that never
// overlap, the hits and the covers wholly shown, which compose joins once
// covers.sync readied them. It keeps its storage from frame to frame.
type scene struct {
	cells cells
	// rows are each row's spans, by x.
	rows [][]span
	hits []hit
	// covers are the covers drawn, in the order drawn, which claims their
	// places (and Kitty's image ids).
	covers []coverRequest
	// buf is the last screen composed, its array the next's.
	buf []byte
}

// reset empties s for a w×h screen measured by c, and returns the
// screen's region.
func (s *scene) reset(w, h int, c cells) region {
	w, h = max(w, 0), max(h, 0)
	s.cells = c
	if h > cap(s.rows) {
		s.rows = slices.Grow(s.rows[:cap(s.rows)], h-cap(s.rows))
	}
	s.rows = s.rows[:h]
	for y := range s.rows {
		s.rows[y] = s.rows[y][:0]
	}
	s.hits, s.covers = s.hits[:0], s.covers[:0]
	screen := image.Rect(0, 0, w, h)
	return region{s: s, bounds: screen, clip: screen}
}

// put adds sp to row y, unless it overlaps a span there; ok reports it
// added.
func (s *scene) put(y int, sp span) (ok bool) {
	i, ok := s.free(y, sp.x, sp.cells)
	switch {
	case !ok:
	case i == len(s.rows[y]):
		s.rows[y] = append(s.rows[y], sp)
	default:
		s.rows[y] = slices.Insert(s.rows[y], i, sp)
	}
	return ok
}

// free reports cells [x, x+n) of row y free, and where a span of them
// goes among the row's.
func (s *scene) free(y, x, n int) (i int, ok bool) {
	row := s.rows[y]
	// Most spans come left to right.
	i = len(row)
	for i > 0 && row[i-1].x > x {
		i--
	}
	isAfterPrev := i == 0 || row[i-1].x+row[i-1].cells <= x
	isBeforeNext := i == len(row) || x+n <= row[i].x
	return i, isAfterPrev && isBeforeNext
}

// overlap drops what r would draw over cells drawn: a layout's mistake,
// which tests make a panic. slot is the cover's, if one.
func (s *scene) overlap(r image.Rectangle, sl slot, isCover bool) {
	if panicOnOverlap {
		panic(fmt.Sprintf("tui: %v drawn over cells drawn", r))
	}
	e := log.Debug().Stringer("rect", r)
	if isCover {
		e = e.Int("slot", int(sl))
	}
	e.Msg("tui: drawing over cells drawn dropped")
}

// compose joins the rows into the screen: their spans from the left with
// spaces between, covers' lines from f, an overlay's line after its mark.
// Rows after the last drawn are left out, which the terminal shows blank.
func (s *scene) compose(f *coverFrame) string {
	last := len(s.rows)
	for last > 0 && len(s.rows[last-1]) == 0 {
		last--
	}
	b := s.buf[:0]
	for y, row := range s.rows[:last] {
		if y > 0 {
			b = append(b, '\n')
		}
		at := 0
		for i := range row {
			sp := &row[i]
			b = appendSpaces(b, sp.x-at)
			at = sp.x + sp.cells
			if sp.isCover {
				b = f.appendLine(b, sp)
				continue
			}
			b = append(b, sp.paint.pre...)
			b = append(b, sp.text...)
			b = append(b, sp.paint.post...)
		}
	}
	s.buf = b
	return string(b)
}

// appendSpaces appends n spaces to b.
func appendSpaces(b []byte, n int) []byte {
	for n > 0 {
		k := min(n, len(spaces))
		b = append(b, spaces[:k]...)
		n -= k
	}
	return b
}

// region is a part of a scene a component draws in, in its own cells
// from (0, 0). bounds is where it is laid out, in the screen's cells,
// past the screen's edges too; clip is the part shown, within its
// parent's. Only a region turns its cells into the screen's.
type region struct {
	s            *scene
	bounds, clip image.Rectangle
}

// size is the region's cells across and down, as laid out.
func (g region) size() (w, h int) { return g.bounds.Dx(), g.bounds.Dy() }

// sub returns the region laid out at r, in g's cells. It may reach past
// g, which shows only its own part of it; a negative size is none.
func (g region) sub(r image.Rectangle) region {
	b := canon(r).Add(g.bounds.Min)
	return region{s: g.s, bounds: b, clip: b.Intersect(g.clip)}
}

// cutTop cuts g's first n rows, at most all, from the rest; both keep
// their place in g's layout, shown as g shows them.
func (g region) cutTop(n int) (top, rest region) {
	w, h := g.size()
	n = min(max(n, 0), h)
	return g.sub(image.Rect(0, 0, w, n)), g.sub(image.Rect(0, n, w, h))
}

// cutLeft cuts g's first n columns, at most all, from the rest, as
// cutTop does rows.
func (g region) cutLeft(n int) (left, rest region) {
	w, h := g.size()
	n = min(max(n, 0), w)
	return g.sub(image.Rect(0, 0, n, h)), g.sub(image.Rect(n, 0, w, h))
}

// inset returns g less l, t, r and b cells on its sides, none negative;
// insets past g's size leave it empty, after the left and top ones.
func (g region) inset(l, t, r, b int) region {
	w, h := g.size()
	x0, y0 := min(max(l, 0), w), min(max(t, 0), h)
	x1, y1 := max(w-max(r, 0), x0), max(h-max(b, 0), y0)
	return g.sub(image.Rect(x0, y0, x1, y1))
}

// text draws s, a line that may hold styles, in p from cell (x, y): the
// cells shown, a wide character cut at their edge as spaces. It returns
// s's width, shown or not.
func (g region) text(x, y int, s string, p paint) int {
	w := g.s.cells.width(s)
	g.putText(x, y, s, w, p)
	return w
}

// putText draws s, which its caller measured w cells wide, as text does.
func (g region) putText(x, y int, s string, w int, p paint) {
	at := g.bounds.Min.Add(image.Pt(x, y))
	r := image.Rect(at.X, at.Y, at.X+w, at.Y+1)
	shown := r.Intersect(g.clip)
	if shown.Empty() {
		return
	}
	if shown.Dx() < w {
		s = g.s.cells.cut(s, shown.Min.X-at.X, shown.Max.X-at.X)
	}
	if !g.s.put(shown.Min.Y, span{x: shown.Min.X, cells: shown.Dx(), text: s, paint: p}) {
		g.s.overlap(shown, 0, false)
	}
}

// fit draws s as text does, fitted to n cells as cells.fit fits it, and
// returns the cells it takes.
func (g region) fit(x, y int, s string, n int, p paint) int {
	c := &g.s.cells
	w := c.width(s)
	if w > n {
		s = c.fit(s, n)
		w = c.width(s)
	}
	g.putText(x, y, s, w, p)
	return w
}

// piece is text in its paint, w cells wide as measured, gap cells after
// what comes before it in a run.
type piece struct {
	text  string
	paint paint
	gap   int
	w     int
}

// measure measures each of ps into its w, and returns the cells they
// take in a run.
func (c *cells) measure(ps []piece) int {
	total := 0
	for i := range ps {
		ps[i].w = c.width(ps[i].text)
		total += ps[i].gap + ps[i].w
	}
	return total
}

// run draws ps one after another from cell (x, y), cut together to n
// cells as cells.fit cuts the line they make: the ellipsis after the
// cells kept is in the paint of the piece it cuts, plain in a gap. It
// returns the cells drawn.
func (g region) run(x, y, n int, ps []piece) int {
	return g.putRun(x, y, n, ps, g.s.cells.measure(ps))
}

// putRun draws ps, which their caller measured total cells wide, as run
// does.
func (g region) putRun(x, y, n int, ps []piece, total int) int {
	c := &g.s.cells
	isCut := total > n
	if isCut && n <= 0 {
		return 0
	}
	// A run cut keeps the cells before the ellipsis.
	keep := n - c.width(ellipsis)
	at := 0
	for i := range ps {
		p := &ps[i]
		start := at + p.gap
		switch {
		case !isCut || start+p.w <= keep:
			g.putText(x+start, y, p.text, p.w, p.paint)
		case start > keep:
			return g.text(x+keep, y, ellipsis, paint{}) + keep
		default:
			// The space makes fit cut a piece that ends on the run's last
			// cell, as the pieces after it would.
			return g.fit(x+start, y, p.text+" ", n-start, p.paint) + start
		}
		at = start + p.w
	}
	return at
}

// line draws s, a line its maker fitted to the cells from x to g's
// right edge, as text does but unmeasured: it holds those cells. The
// parts' lines, until they draw into regions.
func (g region) line(x, y int, s string) {
	at := g.bounds.Min.Add(image.Pt(x, y))
	r := canon(image.Rectangle{Min: at, Max: image.Pt(g.bounds.Max.X, at.Y+1)})
	shown := r.Intersect(g.clip)
	switch {
	case s == "" || shown.Empty():
	case shown != r:
		g.text(x, y, s, paint{})
	case !g.s.put(at.Y, span{x: at.X, cells: r.Dx(), text: s}):
		g.s.overlap(r, 0, false)
	}
}

// hit makes r, in g's cells, h's target: h keeps r whole, as a drag's
// ratio reads it, and its part shown, which clicks hit.
func (g region) hit(r image.Rectangle, h hit) {
	h.rect = canon(r).Add(g.bounds.Min)
	h.shown = h.rect.Intersect(g.clip)
	if !h.shown.Empty() {
		g.s.hits = append(g.s.hits, h)
	}
}

// cover shows art's cover over r, in g's cells, in slot s, if wholly
// shown: a cover cut shows blank. Its cells are the cover's, whose lines
// covers.sync readies.
func (g region) cover(r image.Rectangle, s slot, art catalog.Art) {
	abs := canon(r).Add(g.bounds.Min)
	if art == "" || abs.Empty() || !abs.In(g.clip) {
		return
	}
	sc := g.s
	isTaken := slices.ContainsFunc(sc.covers, func(c coverRequest) bool { return c.slot == s })
	for y := abs.Min.Y; y < abs.Max.Y && !isTaken; y++ {
		_, ok := sc.free(y, abs.Min.X, abs.Dx())
		isTaken = !ok
	}
	if isTaken {
		sc.overlap(abs, s, true)
		return
	}
	for y := abs.Min.Y; y < abs.Max.Y; y++ {
		sc.put(y, span{x: abs.Min.X, cells: abs.Dx(), line: y - abs.Min.Y, slot: s, isCover: true})
	}
	sc.covers = append(sc.covers, coverRequest{rect: abs, art: art, slot: s})
}

// canon is r, a negative size made empty at r.Min, where Canon would
// swap its corners.
func canon(r image.Rectangle) image.Rectangle {
	r.Max = image.Pt(max(r.Max.X, r.Min.X), max(r.Max.Y, r.Min.Y))
	return r
}
