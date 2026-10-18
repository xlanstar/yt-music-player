package termimage

import (
	"image"
	"testing"
)

// encodeCases are a player bar's cover, 4×2 cells of 20×40 pixels (Retina),
// Kitty's placeholders for it, the scaled case, the largest side encoded,
// and real covers, whose bytes the encoders' buffers are sized for. allocs
// is each one's budget (TestBudgetEncode).
var encodeCases = []encodeCase{
	// 2 allocs today: the image and its pixels.
	{"fill/cover80", func(m *image.RGBA) any { return fill(m, 80, 80) }, 128, false, 2},
	// 6 allocs today: fill's 2, the lines and each line's string.
	{"blocks/cover4x2", func(m *image.RGBA) any { return blockLines(fill(m, 4, 4)) }, 64, false, 7},
	// 4 allocs today: the lines, each line's string and their box.
	{"kitty/cells4x2", func(*image.RGBA) any { return kittyCells(176, 4, 2) }, 1, false, 5},
	// 2 allocs today: the string and its box.
	{"kitty/cover80", func(m *image.RGBA) any { return kittyUpload(m, 1, 4, 2, false) }, 80, false, 2},
	// 2 allocs today.
	{"kitty/cover256", func(m *image.RGBA) any { return kittyUpload(m, 1, 8, 4, false) }, 256, false, 2},
	// 9 allocs today: image/jpeg's encoder and tables, the JPEG, and the
	// string and its box.
	{"iterm2/cover80", encodeITerm2, 80, false, 11},
	// 9 allocs today.
	{"iterm2/real160", encodeITerm2, 160, true, 11},
	// 5 allocs today: the placeholders' 3, the upload and the box; fill
	// keeps an image of the size.
	{"encode/kitty-real80", func(m *image.RGBA) any {
		e, _ := Encode(m, Caps{Protocol: Kitty, CellW: 20, CellH: 40}, 4, 2, 176, false)
		return e
	}, 80, true, 6},
	// 4 allocs today: the color indexes, a band's sixels, the string, grown
	// once, and its box in any.
	{"sixel/cover80", encodeSixel, 80, false, 5},
	// 4 allocs today.
	{"sixel/cover256", encodeSixel, 256, false, 5},
	// 4 allocs today.
	{"sixel/real160", encodeSixel, 160, true, 5},
}

type encodeCase struct {
	name   string
	encode func(m *image.RGBA) any
	px     int
	isReal bool
	allocs float64
}

// source is the case's cover: a gradient, or artwork's testdata decoded.
func (tc encodeCase) source(tb testing.TB) *image.RGBA {
	if tc.isReal {
		return realCover(tb, tc.px)
	}
	return cover(tc.px)
}

func encodeITerm2(m *image.RGBA) any { s, _ := iterm2Image(m, 4, 2, false); return s }

func encodeSixel(m *image.RGBA) any { return sixelImage(m) }

var sink any

// BenchmarkEncode encodes a decoded cover as the TUI does once per cover
// and size.
func BenchmarkEncode(b *testing.B) {
	for _, tc := range encodeCases {
		b.Run(tc.name, func(b *testing.B) {
			m := tc.source(b)
			b.SetBytes(int64(len(m.Pix)))
			b.ReportAllocs()
			for b.Loop() {
				sink = tc.encode(m)
			}
		})
	}
}
