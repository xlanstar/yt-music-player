package termimage

import (
	"image"
	"strconv"
	"strings"
)

// bayer is a 4×4 ordered-dither matrix: a fixed palette, dithered, needs
// no per-image quantizing, its maps nor its heap.
var bayer = [4][4]uint8{{0, 8, 2, 10}, {12, 4, 14, 6}, {3, 11, 1, 9}, {15, 7, 13, 5}}

// sixelLevels are the 6×6×6 palette's steps of a channel, in percent.
var sixelLevels = [6]string{"0", "20", "40", "60", "80", "100"}

// SixelImage returns img as a Sixel image, for Overlay to draw, in the 216
// colors of a 6×6×6 cube, ordered-dithered.
func SixelImage(img *image.RGBA) string {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	idx := make([]uint8, w*h)
	var used [216]bool
	for y := range h {
		for x := range w {
			r, g, b := rgb(img, x, y)
			t := bayer[y&3][x&3]
			c := level(r, t)*36 + level(g, t)*6 + level(b, t)
			idx[y*w+x] = c
			used[c] = true
		}
	}
	var s strings.Builder
	s.Grow(w*h/2 + 216*16 + 32)
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
		s.WriteString(";2;")
		s.WriteString(sixelLevels[c/36])
		s.WriteByte(';')
		s.WriteString(sixelLevels[c/6%6])
		s.WriteByte(';')
		s.WriteString(sixelLevels[c%6])
	}
	for y0 := 0; y0 < h; y0 += 6 {
		band := idx[y0*w : min(y0+6, h)*w]
		var inBand [216]bool
		for _, c := range band {
			inBand[c] = true
		}
		first := true
		for c, ok := range inBand {
			if !ok {
				continue
			}
			if !first {
				s.WriteByte('$')
			}
			first = false
			s.WriteByte('#')
			writeInt(&s, c)
			writeSixelRow(&s, band, w, uint8(c))
		}
		s.WriteByte('-')
	}
	s.WriteString("\x1b\\")
	return s.String()
}

// level returns channel v's step of six, dithered by threshold t of 16.
func level(v, t uint8) uint8 {
	scaled := uint16(v) * 5
	l, frac := scaled/255, scaled%255
	if frac*16 > uint16(t)*255+127 {
		l++
	}
	return uint8(l) //nolint:gosec // at most 5
}

// writeSixelRow writes color c's pixels of a band, run-length encoded,
// without the empty run that ends it.
func writeSixelRow(s *strings.Builder, band []uint8, w int, c uint8) {
	rows := len(band) / w
	var n int
	var last byte
	flush := func() {
		switch {
		case n == 0:
		case n > 3:
			s.WriteByte('!')
			writeInt(s, n)
			s.WriteByte(last)
		default:
			for range n {
				s.WriteByte(last)
			}
		}
	}
	for x := range w {
		var bits byte
		for r := range rows {
			if band[r*w+x] == c {
				bits |= 1 << r
			}
		}
		ch := '?' + bits
		if ch == last && n > 0 {
			n++
			continue
		}
		flush()
		last, n = ch, 1
	}
	if last != '?' {
		flush()
	}
}

// writeInt writes n in decimal without the string strconv.Itoa allocates
// past 99.
func writeInt(b *strings.Builder, n int) {
	var num [20]byte
	b.Write(strconv.AppendInt(num[:0], int64(n), 10))
}
