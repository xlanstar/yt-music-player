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
	{"fill/cover80", func(m *image.RGBA) any { return Fill(m, 80, 80) }, 128, false, 2},
	// 6 allocs today: Fill's 2, the lines and each line's string.
	{"blocks/cover4x2", func(m *image.RGBA) any { return BlockLines(Fill(m, 4, 4)) }, 64, false, 7},
	// 5 allocs today: the color, the lines and each line's string.
	{"kitty/cells4x2", func(*image.RGBA) any { return KittyCells(176, 4, 2) }, 1, false, 6},
	// 3 allocs today: the first command's keys and the string.
	{"kitty/cover80", func(m *image.RGBA) any { return KittyUpload(m, 1, 4, 2, false) }, 80, false, 4},
	// 5 allocs today: numbers past 99 take theirs.
	{"kitty/cover256", func(m *image.RGBA) any { return KittyUpload(m, 1, 8, 4, false) }, 256, false, 6},
	// 9 allocs today: image/jpeg's encoder and tables, the JPEG, and the
	// string and its box.
	{"iterm2/cover80", encodeITerm2, 80, false, 11},
	// 9 allocs today.
	{"iterm2/real160", encodeITerm2, 160, true, 11},
	// 8 allocs today: the placeholders' 5, the upload's 2 and the box;
	// Fill keeps an image of the size.
	{"encode/kitty-real80", func(m *image.RGBA) any {
		e, _ := Encode(m, Caps{Protocol: Kitty, CellW: 20, CellH: 40}, 4, 2, 176, false)
		return e
	}, 80, true, 10},
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

func encodeITerm2(m *image.RGBA) any { s, _ := ITerm2Image(m, 4, 2, false); return s }

func encodeSixel(m *image.RGBA) any { return SixelImage(m) }

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
