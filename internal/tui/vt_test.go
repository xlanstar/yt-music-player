package tui

import (
	"fmt"
	"image"
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// vt follows a terminal through what ytm wrote it, as far as the renderer
// and termOut write: the cells, the cursor, autowrap, the scroll region,
// and which cells each synchronized frame wrote, erased or moved, and the
// images it drew. It errs on what it does not know, so that a Bubble Tea
// that starts writing something new is read, never guessed.
//
// Lines end as the renderer believes they do over a pipe: a newline
// returns the cursor too (the tty's ONLCR).
type vt struct {
	w, h int
	// cells are the alternate screen's: a grapheme, "" right of a wide one.
	cells    [][]string
	x, y     int
	saved    image.Point
	top, bot int
	noWrap   bool
	// graphemes: text is measured by grapheme (mode 2027), else by rune.
	graphemes bool
	pending   bool
	// isCursorShown follows mode 25, shown until hidden.
	isCursorShown bool
	alt           bool
	last          string
	inFrame       bool
	touched       [][]bool
	images        []vtImage
	wroteAfter    bool
	frames        []vtFrame
	mainImages    int
}

// vtImage is an image drawn at a cell, from 0.
type vtImage struct{ at image.Point }

// vtFrame is what one synchronized frame did.
type vtFrame struct {
	// touched are the cells it wrote, erased or moved, by line.
	touched [][]bool
	images  []vtImage
	// cells are the screen's once it was written.
	cells [][]string
	// wroteAfter reports cells written after an image.
	wroteAfter bool
	// cursor is where the cursor ended, if isCursorShown.
	cursor        image.Point
	isCursorShown bool
	// text is the frame as written, for failures.
	text string
}

func newVT(w, h int) *vt {
	v := &vt{isCursorShown: true}
	v.resize(w, h)
	return v
}

// resize makes the screen w×h cells, blank.
func (v *vt) resize(w, h int) {
	v.w, v.h, v.top, v.bot = w, h, 0, h-1
	v.cells = make([][]string, h)
	for y := range v.cells {
		v.cells[y] = blanks(w)
	}
	v.x, v.y = min(v.x, w-1), min(v.y, h-1)
}

// blanks is a line of n blank cells.
func blanks(n int) []string { return slices.Repeat([]string{" "}, n) }

// screen is the alternate screen's text, its lines' trailing spaces cut.
func (v *vt) screen() string {
	var b strings.Builder
	for _, row := range v.cells {
		b.WriteString(strings.TrimRight(strings.Join(row, ""), " "))
		b.WriteByte('\n')
	}
	return b.String()
}

func (v *vt) touch(x0, x1, y int) {
	if !v.inFrame || y < 0 || y >= v.h {
		return
	}
	v.wroteAfter = v.wroteAfter || len(v.images) > 0
	for x := max(x0, 0); x < min(x1, v.w); x++ {
		v.touched[y][x] = true
	}
}

func (v *vt) erase(x0, x1, y int) {
	v.touch(x0, x1, y)
	for x := max(x0, 0); x < min(x1, v.w); x++ {
		v.cells[y][x] = " "
	}
}

// scroll moves lines top to bot up by n, down when n < 0.
func (v *vt) scroll(top, bot, n int) {
	for y := top; y <= bot; y++ {
		v.touch(0, v.w, y)
	}
	rows := v.cells[top : bot+1]
	for range abs(n) {
		if n > 0 {
			copy(rows, rows[1:])
			rows[len(rows)-1] = blanks(v.w)
		} else {
			copy(rows[1:], rows)
			rows[0] = blanks(v.w)
		}
	}
}

func abs(n int) int { return max(n, -n) }

func (v *vt) lineFeed() {
	v.pending = false
	if v.y == v.bot {
		v.scroll(v.top, v.bot, 1)
	} else if v.y < v.h-1 {
		v.y++
	}
}

func (v *vt) moveTo(x, y int) {
	v.x, v.y, v.pending = min(max(x, 0), v.w-1), min(max(y, 0), v.h-1), false
}

func (v *vt) print(g string, width int) {
	if v.pending {
		v.x = 0
		v.lineFeed()
	}
	if v.x+width > v.w {
		if v.noWrap {
			v.x = v.w - width
		} else {
			v.x = 0
			v.lineFeed()
		}
	}
	v.touch(v.x, v.x+width, v.y)
	v.cells[v.y][v.x] = g
	for i := 1; i < width; i++ {
		v.cells[v.y][v.x+i] = ""
	}
	v.last = g
	if v.x += width; v.x >= v.w {
		v.x, v.pending = v.w-1, !v.noWrap
	}
}

// write reads b, which must hold whole sequences.
func (v *vt) write(b string) error {
	p := ansi.NewParser()
	var state byte
	frameStart := 0
	for i := 0; i < len(b); {
		decode := ansi.DecodeSequenceWc[string]
		if v.graphemes {
			decode = ansi.DecodeSequence[string]
		}
		seq, width, n, next := decode(b[i:], state, p)
		state = next
		if n == 1 && seq[0] > 0x1f && seq[0] < 0x7f && i+1 < len(b) && b[i+1] >= 0xc0 {
			// As the renderer does: a letter takes the marks after it.
			seq, width = ansi.FirstGraphemeCluster(b[i:], v.method())
			n = len(seq)
		}
		if width > 0 {
			v.print(seq, width)
		} else if err := v.control(seq, p); err != nil {
			return fmt.Errorf("at %d of %q: %w", i, b[max(i-40, 0):min(i+40, len(b))], err)
		}
		i += n
		switch seq {
		case "\x1b[?2026h":
			v.inFrame, frameStart = true, i
			v.touched = make([][]bool, v.h)
			for y := range v.touched {
				v.touched[y] = make([]bool, v.w)
			}
			v.images, v.wroteAfter = nil, false
		case "\x1b[?2026l":
			v.inFrame = false
			cells := make([][]string, v.h)
			for y, row := range v.cells {
				cells[y] = slices.Clone(row)
			}
			v.frames = append(v.frames, vtFrame{
				touched: v.touched, images: v.images, cells: cells, wroteAfter: v.wroteAfter,
				cursor: image.Pt(v.x, v.y), isCursorShown: v.isCursorShown, text: b[frameStart:i],
			})
		}
	}
	return nil
}

func (v *vt) control(seq string, p *ansi.Parser) error {
	switch {
	case seq == "\r":
		v.x, v.pending = 0, false
	case seq == "\n":
		v.x = 0
		v.lineFeed()
	case seq == "\b":
		v.moveTo(v.x-1, v.y)
	case seq == "\x1b7":
		v.saved = image.Pt(v.x, v.y)
	case seq == "\x1b8":
		v.moveTo(v.saved.X, v.saved.Y)
	case seq == "\x1bM":
		if v.y == v.top {
			v.scroll(v.top, v.bot, -1)
		} else {
			v.moveTo(v.x, v.y-1)
		}
	case ansi.HasCsiPrefix(seq):
		return v.csi(ansi.Cmd(p.Command()), p.Params())
	case ansi.HasDcsPrefix(seq):
		if ansi.Cmd(p.Command()).Final() != 'q' {
			return fmt.Errorf("DCS %q", seq)
		}
		v.image()
	case ansi.HasOscPrefix(seq):
		if p.Command() == 1337 {
			v.image()
		}
	case ansi.HasPmPrefix(seq):
		return fmt.Errorf("a mark reached the terminal: %q", seq)
	case ansi.HasApcPrefix(seq), ansi.HasSosPrefix(seq):
	case len(seq) == 1 && seq[0] < 0x20, seq == "\x7f":
	default:
		return fmt.Errorf("unknown %q", seq)
	}
	return nil
}

// method is how v measures text.
func (v *vt) method() ansi.Method {
	if v.graphemes {
		return ansi.GraphemeWidth
	}
	return ansi.WcWidth
}

func (v *vt) image() {
	if !v.alt {
		v.mainImages++
	}
	v.images = append(v.images, vtImage{at: image.Pt(v.x, v.y)})
}

func (v *vt) csi(cmd ansi.Cmd, params ansi.Params) error {
	n := func(i int) int {
		p, _, _ := params.Param(i, 1)
		return max(p, 1)
	}
	at := func(i, def int) int {
		p, _, _ := params.Param(i, def)
		return p
	}
	if cmd.Prefix() == '?' {
		return v.mode(cmd, params)
	}
	if cmd.Prefix() != 0 || cmd.Intermediate() != 0 {
		// Keyboard and cursor-style settings, queries.
		return nil
	}
	switch cmd.Final() {
	case 'H', 'f':
		v.moveTo(n(1)-1, n(0)-1)
	case 'A':
		v.moveTo(v.x, v.y-n(0))
	case 'B':
		v.moveTo(v.x, v.y+n(0))
	case 'C':
		v.moveTo(v.x+n(0), v.y)
	case 'D':
		v.moveTo(v.x-n(0), v.y)
	case 'E':
		v.moveTo(0, v.y+n(0))
	case 'F':
		v.moveTo(0, v.y-n(0))
	case 'G', '`':
		v.moveTo(n(0)-1, v.y)
	case 'd':
		v.moveTo(v.x, n(0)-1)
	case 'K':
		switch at(0, 0) {
		case 0:
			v.erase(v.x, v.w, v.y)
		case 1:
			v.erase(0, v.x+1, v.y)
		default:
			v.erase(0, v.w, v.y)
		}
	case 'J':
		y0, y1 := 0, v.h
		switch at(0, 0) {
		case 0:
			v.erase(v.x, v.w, v.y)
			y0 = v.y + 1
		case 1:
			v.erase(0, v.x+1, v.y)
			y1 = v.y
		}
		for y := y0; y < y1; y++ {
			v.erase(0, v.w, y)
		}
	case 'X':
		v.erase(v.x, v.x+n(0), v.y)
	case '@', 'P':
		v.touch(v.x, v.w, v.y)
		row, k := v.cells[v.y], min(n(0), v.w-v.x)
		if cmd.Final() == '@' {
			copy(row[v.x+k:], row[v.x:])
			copy(row[v.x:v.x+k], blanks(k))
		} else {
			copy(row[v.x:], row[v.x+k:])
			copy(row[v.w-k:], blanks(k))
		}
	case 'L', 'M':
		if v.y >= v.top && v.y <= v.bot {
			k := n(0)
			if cmd.Final() == 'L' {
				k = -k
			}
			v.scroll(v.y, v.bot, k)
			v.x = 0
		}
	case 'S':
		v.scroll(v.top, v.bot, n(0))
	case 'T':
		v.scroll(v.top, v.bot, -n(0))
	case 'r':
		v.top, v.bot = n(0)-1, at(1, v.h)-1
		v.moveTo(0, 0)
	case 'b':
		for range n(0) {
			v.print(v.last, max(v.method().StringWidth(v.last), 1))
		}
	case 'm', 't', 'n', 'c', 'g':
	default:
		return fmt.Errorf("CSI %c", cmd.Final())
	}
	return nil
}

func (v *vt) mode(cmd ansi.Cmd, params ansi.Params) error {
	if cmd.Intermediate() != 0 || (cmd.Final() != 'h' && cmd.Final() != 'l') {
		// Queries.
		return nil
	}
	set := cmd.Final() == 'h'
	for _, p := range params {
		switch p.Param(0) {
		case 7:
			v.noWrap = !set
		case 25:
			v.isCursorShown = set
		case 2027:
			v.graphemes = set
		case 1049:
			v.alt = set
			if set {
				v.saved = image.Pt(v.x, v.y)
				v.resize(v.w, v.h)
			} else {
				v.moveTo(v.saved.X, v.saved.Y)
			}
		}
	}
	return nil
}
