package termimage

import (
	"bytes"
	"strconv"
)

// markStart opens a Mark: a PM, which a cell renderer such as ultraviolet
// keeps in the content of the cell after it and writes only with that cell.
const markStart = "\x1b^ytm;"

// stringEnd is ST, which ends a mark.
const stringEnd = "\x1b\\"

// A frame's end, which overlays go before: the synchronized output's, else
// showing the cursor, which would flicker over them.
const (
	syncEnd    = "\x1b[?2026l"
	cursorShow = "\x1b[?25h"
)

// maxSlots bounds a Table's slots: Splice keeps those drawn in a uint64.
const maxSlots = 64

var markPrefix = []byte(markStart)

// Mark returns what goes before the first cell of an overlay's line: row
// of slot under the Table gen. A frame that writes the cell writes the
// mark, telling Splice it wrote over the image. The row keeps a cover's
// lines unlike each other and any other line, so a renderer never scrolls
// one into place instead of writing it.
func Mark(gen uint64, slot, row int) string {
	b := make([]byte, 0, len(markStart)+3*20+len(stringEnd))
	b = append(b, markStart...)
	b = strconv.AppendUint(b, gen, 10)
	b = append(b, ';')
	b = strconv.AppendInt(b, int64(slot), 10)
	b = append(b, ';')
	b = strconv.AppendInt(b, int64(row), 10)
	b = append(b, stringEnd...)
	return string(b)
}

// Placement is an overlay a Table draws: Seq, an Encoded.Overlay, over
// Cols×Rows cells from cell X, Y (from 0).
type Placement struct {
	Seq              string
	X, Y, Cols, Rows int
}

// fits reports p inside a w×h screen and above its last line, where a Sixel
// image would scroll the screen.
func (p Placement) fits(w, h int) bool {
	return p.X >= 0 && p.Y >= 0 && p.X+p.Cols <= w && p.Y+p.Rows < h
}

// Table is a generation of overlays, indexed by slot; a zero Placement is
// none. It never changes once made, so a writer may read it while the UI
// makes the next.
type Table struct {
	gen    uint64
	places []Placement
}

// NewTable returns the Table gen of places, at most 64 slots.
func NewTable(gen uint64, places []Placement) *Table {
	if len(places) > maxSlots {
		panic("termimage: more than 64 slots")
	}
	return &Table{gen: gen, places: append([]Placement(nil), places...)}
}

// Gen returns the generation the Table's marks carry.
func (t *Table) Gen() uint64 { return t.gen }

// FrameGen returns the generation of frame's first mark, false without one.
func FrameGen(frame []byte) (gen uint64, ok bool) {
	for rest := frame; ; {
		i := bytes.Index(rest, markPrefix)
		if i < 0 {
			return 0, false
		}
		if gen, _, n := parseMark(rest[i:]); n > 0 {
			return gen, true
		}
		rest = rest[i+len(markPrefix):]
	}
}

// Splice appends frame to dst without its marks, then the overlays of t's
// slots whose marks frame wrote, before the frame's end: the image goes
// over the cells the frame just wrote, once whichever of its lines did.
// Marks of another generation, or with t nil, draw nothing; an overlay
// draws only where it fits a w×h screen (Placement.fits).
func Splice(dst, frame []byte, t *Table, w, h int) []byte {
	end := len(frame)
	switch {
	case bytes.HasSuffix(frame, []byte(syncEnd)):
		end -= len(syncEnd)
	case bytes.HasSuffix(frame, []byte(cursorShow)):
		end -= len(cursorShow)
	}
	var drawn uint64
	rest := frame[:end]
	for {
		i := bytes.Index(rest, markPrefix)
		if i < 0 {
			break
		}
		dst = append(dst, rest[:i]...)
		gen, slot, n := parseMark(rest[i:])
		if n == 0 {
			// Not a mark: another program's PM, passed on.
			n = len(markPrefix)
			dst = append(dst, rest[i:i+n]...)
		} else if t != nil && gen == t.gen && slot < len(t.places) {
			drawn |= 1 << slot
		}
		rest = rest[i+n:]
	}
	dst = append(dst, rest...)
	if t != nil {
		for slot, p := range t.places {
			if drawn&(1<<slot) != 0 && p.Seq != "" && p.fits(w, h) {
				dst = appendOverlay(dst, p.Seq, p.X, p.Y)
			}
		}
	}
	return append(dst, frame[end:]...)
}

// parseMark reads the mark b starts with: its generation, its slot and its
// length; 0 if b starts no mark.
func parseMark(b []byte) (gen uint64, slot, n int) {
	i := len(markPrefix)
	gen, i, ok := parseUint(b, i, ';')
	if !ok {
		return 0, 0, 0
	}
	s, i, ok := parseUint(b, i, ';')
	if !ok || s >= maxSlots {
		return 0, 0, 0
	}
	if _, i, ok = parseUint(b, i, stringEnd[0]); !ok || i >= len(b) || b[i] != stringEnd[1] {
		return 0, 0, 0
	}
	return gen, int(s), i + 1
}

// parseUint reads the decimal at b[i:] up to end, and returns it and the
// index past end.
func parseUint(b []byte, i int, end byte) (n uint64, next int, ok bool) {
	start := i
	for ; i < len(b) && b[i] >= '0' && b[i] <= '9'; i++ {
		n = n*10 + uint64(b[i]-'0')
	}
	if i == start || i-start > 20 || i >= len(b) || b[i] != end {
		return 0, 0, false
	}
	return n, i + 1, true
}

// appendOverlay appends seq drawn at cell x, y (from 0), the cursor kept
// where it was: the renderer moves it by what it believes.
func appendOverlay(dst []byte, seq string, x, y int) []byte {
	dst = append(dst, "\x1b7\x1b["...)
	dst = strconv.AppendInt(dst, int64(y+1), 10)
	dst = append(dst, ';')
	dst = strconv.AppendInt(dst, int64(x+1), 10)
	dst = append(dst, 'H')
	dst = append(dst, seq...)
	return append(dst, "\x1b8"...)
}
