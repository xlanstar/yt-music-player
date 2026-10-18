package termimage

import (
	"image"
	"strings"
)

// blockLines returns img, cols×2rows pixels, as rows lines of upper half
// blocks, each cell its top pixel over its bottom one, in true color; the
// renderer lowers them to what the terminal shows.
func blockLines(img *image.RGBA) []string {
	cols, rows := img.Rect.Dx(), img.Rect.Dy()/2
	lines := make([]string, rows)
	var b strings.Builder
	for y := range rows {
		b.Grow(cols*len("\x1b[38;2;255;255;255;48;2;255;255;255m▀") + len("\x1b[m"))
		for x := range cols {
			r, g, bl := rgb(img, x, 2*y)
			b.WriteString("\x1b[38;2;")
			writeRGB(&b, int(r), int(g), int(bl))
			b.WriteString(";48;2;")
			r, g, bl = rgb(img, x, 2*y+1)
			writeRGB(&b, int(r), int(g), int(bl))
			b.WriteString("m▀")
		}
		b.WriteString("\x1b[m")
		lines[y] = b.String()
		b.Reset()
	}
	return lines
}

// writeRGB writes r;g;b, as an SGR color or a Sixel palette's.
func writeRGB(b *strings.Builder, r, g, bl int) {
	writeInt(b, r)
	b.WriteByte(';')
	writeInt(b, g)
	b.WriteByte(';')
	writeInt(b, bl)
}
