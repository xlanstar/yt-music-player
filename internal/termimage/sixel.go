package termimage

import (
	"bytes"
	"image"
	"strings"
)

// bayer is a 4×4 ordered-dither matrix: a fixed palette, dithered, needs
// no per-image quantizing, its maps nor its heap.
var bayer = [4][4]uint8{{0, 8, 2, 10}, {12, 4, 14, 6}, {3, 11, 1, 9}, {15, 7, 13, 5}}

// sixelImage returns img as a Sixel image, for Overlay to draw, in the 216
// colors of a 6×6×6 cube, ordered-dithered.
func sixelImage(img *image.RGBA) string {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	idx := make([]uint8, w*h)
	var used [216]bool
	colors := 0
	for y := range h {
		for x := range w {
			r, g, b := rgb(img, x, y)
			l := &levels[bayer[y&3][x&3]]
			c := l[r]*36 + l[g]*6 + l[b]
			idx[y*w+x] = c
			if !used[c] {
				used[c] = true
				colors++
			}
		}
	}
	var s strings.Builder
	// Covers take 0.7–1.25 bytes a pixel, and the palette's.
	s.Grow(w*h*3/4 + 216*16 + 32)
	s.WriteString("\x1bP0;1;0q\"1;1;")
	writeInt(&s, w)
	s.WriteByte(';')
	writeInt(&s, h)
	for c, ok := range used {
		if !ok {
			continue
		}
		s.WriteByte('#')
		writeInt(&s, c)
		// The cube's steps, in percent.
		s.WriteString(";2;")
		writeRGB(&s, c/36*20, c/6%6*20, c%6*20)
	}
	// A band's sixels, a row of w per color in it, made in one pass.
	sixels := make([]byte, min(colors, 6*w)*w)
	for y0 := 0; y0 < h; y0 += 6 {
		band := idx[y0*w : min(y0+6, h)*w]
		// row is a color's row of sixels, plus one; 0 is none.
		var row [216]uint8
		n := 0
		for r := range len(band) / w {
			for x, c := range band[r*w : (r+1)*w] {
				if row[c] == 0 {
					n++
					row[c] = uint8(n)
					clear(sixels[(n-1)*w : n*w])
				}
				sixels[int(row[c]-1)*w+x] |= 1 << r
			}
		}
		first := true
		for c, i := range row {
			if i == 0 {
				continue
			}
			if !first {
				s.WriteByte('$')
			}
			first = false
			s.WriteByte('#')
			writeInt(&s, c)
			writeSixelRow(&s, sixels[int(i-1)*w:int(i)*w])
		}
		s.WriteByte('-')
	}
	s.WriteString("\x1b\\")
	return s.String()
}

// levels[t][v] is level(v, t).
var levels = func() (l [16][256]uint8) {
	for t := range l {
		for v := range l[t] {
			l[t][v] = level(uint8(v), uint8(t))
		}
	}
	return l
}()

// level returns channel v's step of six, dithered by threshold t of 16.
func level(v, t uint8) uint8 {
	scaled := uint16(v) * 5
	l, frac := scaled/255, scaled%255
	if frac*16 > uint16(t)*255+127 {
		l++
	}
	return uint8(l) //nolint:gosec // at most 5
}

// writeSixelRow writes a color's sixels of a band, run-length encoded,
// without the empty run that ends it.
func writeSixelRow(s *strings.Builder, sixels []byte) {
	sixels = bytes.TrimRight(sixels, "\x00")
	for i := 0; i < len(sixels); {
		j := i + 1
		for j < len(sixels) && sixels[j] == sixels[i] {
			j++
		}
		ch := '?' + sixels[i]
		if n := j - i; n > 3 {
			s.WriteByte('!')
			writeInt(s, n)
			s.WriteByte(ch)
		} else {
			for range n {
				s.WriteByte(ch)
			}
		}
		i = j
	}
}
