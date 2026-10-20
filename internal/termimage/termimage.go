// Package termimage shows small covers in a terminal: it tells which protocol
// the terminal speaks and encodes an opaque *image.RGBA for it. It does no I/O
// and knows no TUI: a caller writes Probe if NeedsProbe, feeds the replies to
// Detect, and writes what Encode returns.
//
// Kitty's Unicode placeholders and half blocks are text, which a cell renderer
// diffs like any other. Sixel and iTerm2 images are pixels over cells, which a
// write to any of them wipes: the caller shows Encoded.Lines, blank, each line's
// first cell after a Mark, and passes every frame the renderer writes through
// Splice, which draws after it the images whose marks it wrote. A cover's cells
// change only with its mark, and a renderer writes a line from its first
// changed cell, or whole: a frame that wipes an image writes its mark.
//
// Encoders take what artwork decodes, opaque and small, and read Pix directly:
// x/ansi's general encoders box every pixel through image.Image.At.
package termimage

import (
	"image"
	"slices"
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

func (p Protocol) String() string { return protocolNames[p] }

// maxPixels bounds the side of what Kitty and iTerm2 get, which they scale.
const maxPixels = 256

// Caps is what Detect found.
type Caps struct {
	Protocol Protocol
	// CellW and CellH are a cell's pixels, 0 when the terminal did not say.
	CellW, CellH int
}

// cell returns a cell's pixels, assuming 10×20 when unknown.
func (c Caps) cell() (w, h int) {
	if c.CellW > 0 && c.CellH > 0 {
		return c.CellW, c.CellH
	}
	return 10, 20
}

// Fit returns how many columns a square image rows cells tall spans.
func (c Caps) Fit(rows int) int {
	w, h := c.cell()
	cols := (rows*h + w/2) / w
	return min(max(cols, rows), 4*rows)
}

// Pixels returns the size to encode an image of cols×rows cells at; the
// terminal scales Kitty and iTerm2 images.
func (c Caps) Pixels(cols, rows int) (w, h int) {
	if c.Protocol == Blocks {
		return cols, 2 * rows
	}
	cw, ch := c.cell()
	w, h = cols*cw, rows*ch
	if c.Protocol == Sixel {
		// Sixel pixels are the cells': a smaller image would not fill them.
		// Its bands are 6 pixels, which a terminal paints whole, past the cells.
		if h >= 6 {
			h -= h % 6
		}
		return w, h
	}
	if side := max(w, h); side > maxPixels {
		w, h = max(w*maxPixels/side, 1), max(h*maxPixels/side, 1)
	}
	return w, h
}

// Encoded is an image encoded for a terminal.
type Encoded struct {
	// Lines are what its cells show: half blocks, Kitty's placeholders, or
	// an overlay's blank.
	Lines []string
	// Upload is sent once: Kitty's pixels, which the terminal keeps.
	Upload string
	// Overlay is drawn over Lines after each repaint: Sixel or iTerm2.
	Overlay string
}

// Encode encodes src for c's protocol over cols×rows cells, filled to
// Pixels; id names a Kitty image, tmux passes sequences through.
func Encode(src *image.RGBA, c Caps, cols, rows int, id uint8, tmux bool) (Encoded, error) {
	w, h := c.Pixels(cols, rows)
	img := fill(src, w, h)
	switch c.Protocol {
	case Blocks:
		return Encoded{Lines: blockLines(img)}, nil
	case Kitty:
		return Encoded{Lines: kittyCells(id, cols, rows), Upload: kittyUpload(img, id, cols, rows, tmux)}, nil
	case Sixel:
		return Encoded{Lines: overlayCells(cols, rows), Overlay: sixelImage(img)}, nil
	case ITerm2:
		seq, err := iterm2Image(img, cols, rows, tmux)
		return Encoded{Lines: overlayCells(cols, rows), Overlay: seq}, err
	}
	return Encoded{}, nil
}

// passthrough wraps seq for tmux to hand to the terminal around it, which
// needs `set -g allow-passthrough on`.
func passthrough(seq string) string {
	return "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
}

// OverlayFrame is the most Overlay writes besides seq.
const OverlayFrame = len("\x1b7\x1b[999;999H\x1b8")

// Overlay writes seq drawn at cell x, y (from 0), the cursor kept where it
// was: the renderer moves it by what it believes.
func Overlay(b *strings.Builder, seq string, x, y int) {
	b.WriteString("\x1b7\x1b[")
	writeInt(b, y+1)
	b.WriteByte(';')
	writeInt(b, x+1)
	b.WriteByte('H')
	b.WriteString(seq)
	b.WriteString("\x1b8")
}

// overlayCells returns the lines an overlay covers: blank, but braille's
// blank rather than spaces, so the renderer repaints them, wiping the
// image, once the image goes. It never prints them to move the cursor:
// 3 bytes a cell cost more than a cursor move.
func overlayCells(cols, rows int) []string {
	return slices.Repeat([]string{strings.Repeat("\u2800", cols)}, rows)
}

// rgb returns the pixel at x, y of an opaque image.
func rgb(m *image.RGBA, x, y int) (r, g, b uint8) {
	p := m.Pix[y*m.Stride+4*x:]
	return p[0], p[1], p[2]
}

// writeInt writes n in decimal without the string strconv.Itoa allocates
// past 99.
func writeInt(b *strings.Builder, n int) {
	var num [20]byte
	b.Write(strconv.AppendInt(num[:0], int64(n), 10))
}
