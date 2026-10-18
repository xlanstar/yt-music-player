// Package termimage shows small covers in a terminal: it tells which protocol
// the terminal speaks and encodes an opaque *image.RGBA for it. It does no I/O
// and knows no TUI: a caller writes Probe, feeds the replies to Detect, and
// writes what the encoders return.
//
// Kitty's Unicode placeholders and half blocks are text, which a cell renderer
// diffs like any other. Sixel and iTerm2 images are pixels over cells: the
// caller leaves those cells blank (OverlayCells) and draws the image after the
// frame that blanked them (Overlay), and again after a full repaint.
//
// Encoders take what artwork decodes, opaque and small, and read Pix directly:
// x/ansi's general encoders box every pixel through image.Image.At.
package termimage

import (
	"image"
	"strconv"
	"strings"
)

// Protocol is how a terminal shows images.
type Protocol uint8

// Protocols, from none to the best Detect picks.
const (
	None Protocol = iota
	Blocks
	Sixel
	ITerm2
	Kitty
)

var protocolNames = [...]string{None: "off", Blocks: "blocks", Sixel: "sixel", ITerm2: "iterm2", Kitty: "kitty"}

func (p Protocol) String() string {
	if int(p) < len(protocolNames) {
		return protocolNames[p]
	}
	return "Protocol(" + strconv.Itoa(int(p)) + ")"
}

// IsOverlay reports pixels drawn over blank cells rather than text.
func (p Protocol) IsOverlay() bool { return p == Sixel || p == ITerm2 }

// Caps is what Detect found.
type Caps struct {
	Protocol Protocol
	// CellW and CellH are a cell's pixels, 0 when the terminal did not say.
	CellW, CellH int
	// Tmux wraps sequences tmux would otherwise eat (Passthrough).
	Tmux bool
}

// Cell returns a cell's pixels, assuming 10×20 when unknown.
func (c Caps) Cell() (w, h int) {
	if c.CellW > 0 && c.CellH > 0 {
		return c.CellW, c.CellH
	}
	return 10, 20
}

// Fit returns how many columns a square image rows cells tall spans.
func (c Caps) Fit(rows int) int {
	w, h := c.Cell()
	cols := (rows*h + w/2) / w
	return min(max(cols, rows), 4*rows)
}

// Pixels returns the size, at most maxSide on a side, to encode an image
// of cols×rows cells at; the terminal scales Kitty and iTerm2 images.
func (c Caps) Pixels(cols, rows, maxSide int) (w, h int) {
	if c.Protocol == Blocks {
		return cols, 2 * rows
	}
	cw, ch := c.Cell()
	w, h = cols*cw, rows*ch
	if c.Protocol == Sixel {
		// Sixel pixels are the cells': a smaller image would not fill them.
		return w, h
	}
	if side := max(w, h); side > maxSide {
		w, h = max(w*maxSide/side, 1), max(h*maxSide/side, 1)
	}
	return w, h
}

// Passthrough wraps seq for tmux to hand to the terminal around it, which
// needs `set -g allow-passthrough on`.
func Passthrough(seq string) string {
	return "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
}

// Overlay returns seq drawn at cell x, y (from 0), the cursor kept where it
// was: the renderer moves it by what it believes.
func Overlay(seq string, x, y int) string {
	var b strings.Builder
	b.Grow(len(seq) + 16)
	b.WriteString("\x1b7\x1b[")
	b.WriteString(strconv.Itoa(y + 1))
	b.WriteByte(';')
	b.WriteString(strconv.Itoa(x + 1))
	b.WriteByte('H')
	b.WriteString(seq)
	b.WriteString("\x1b8")
	return b.String()
}

// OverlayCells returns the lines an overlay covers: blank, but braille's
// blank rather than spaces, so the renderer repaints them, wiping the
// image, once the image goes. It never prints them to move the cursor:
// 3 bytes a cell cost more than a cursor move.
func OverlayCells(cols, rows int) []string {
	line := strings.Repeat("\u2800", cols)
	lines := make([]string, rows)
	for i := range lines {
		lines[i] = line
	}
	return lines
}

// rgb returns the pixel at x, y of an opaque image.
func rgb(m *image.RGBA, x, y int) (r, g, b uint8) {
	p := m.Pix[y*m.Stride+4*x:]
	return p[0], p[1], p[2]
}
