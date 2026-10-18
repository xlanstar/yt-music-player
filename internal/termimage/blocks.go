package termimage

import (
	"image"
	"strconv"
	"strings"
)

// BlockLines returns img, cols×2rows pixels, as rows lines of upper half
// blocks, each cell its top pixel over its bottom one, in true color; the
// renderer lowers them to what the terminal shows.
func BlockLines(img *image.RGBA) []string {
	cols, rows := img.Rect.Dx(), img.Rect.Dy()/2
	lines := make([]string, rows)
	var b strings.Builder
	for y := range rows {
		b.Grow(cols*len("\x1b[38;2;255;255;255;48;2;255;255;255m▀") + len("\x1b[m"))
		for x := range cols {
			r, g, bl := rgb(img, x, 2*y)
			b.WriteString("\x1b[38;2;")
			writeRGB(&b, r, g, bl)
			b.WriteString(";48;2;")
			r, g, bl = rgb(img, x, 2*y+1)
			writeRGB(&b, r, g, bl)
			b.WriteString("m▀")
		}
		b.WriteString("\x1b[m")
		lines[y] = b.String()
		b.Reset()
	}
	return lines
}

func writeRGB(b *strings.Builder, r, g, bl uint8) {
	var num [3]byte
	b.Write(strconv.AppendUint(num[:0], uint64(r), 10))
	b.WriteByte(';')
	b.Write(strconv.AppendUint(num[:0], uint64(g), 10))
	b.WriteByte(';')
	b.Write(strconv.AppendUint(num[:0], uint64(bl), 10))
}
