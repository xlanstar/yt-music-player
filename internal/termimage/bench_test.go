package termimage

import (
	"image"
	"testing"
)

// encodeCases are a player bar's cover, 4×2 cells of 20×40 pixels (Retina),
// and the scaled case, the largest side encoded. allocs is each one's
// budget (TestBudgetEncode).
var encodeCases = []struct {
	name   string
	encode func(m *image.RGBA) any
	px     int
	allocs float64
}{
	// 2 allocs today: the image and its pixels.
	{"fill/cover80", func(m *image.RGBA) any { return Fill(m, 80, 80) }, 128, 2},
	// 6 allocs today: Fill's 2, the lines and each line's string.
	{"blocks/cover4x2", func(m *image.RGBA) any { return BlockLines(Fill(m, 4, 4)) }, 64, 7},
	// 3 allocs today: the first command's keys and the string.
	{"kitty/cover80", func(m *image.RGBA) any { return KittyUpload(m, 1, 4, 2, false) }, 80, 4},
	// 5 allocs today: numbers past 99 take theirs.
	{"kitty/cover256", func(m *image.RGBA) any { return KittyUpload(m, 1, 8, 4, false) }, 256, 6},
	// 14 allocs today: image/jpeg's encoder and buffer, and the string.
	{"iterm2/cover80", func(m *image.RGBA) any { s, _ := ITerm2Image(m, 4, 2, false); return s }, 80, 17},
	// 3 allocs today: the color indexes and the string, grown once.
	{"sixel/cover80", func(m *image.RGBA) any { return SixelImage(m) }, 80, 4},
	// 4 allocs today.
	{"sixel/cover256", func(m *image.RGBA) any { return SixelImage(m) }, 256, 5},
}

var sink any

// BenchmarkEncode encodes a decoded cover as the TUI does once per cover
// and size.
func BenchmarkEncode(b *testing.B) {
	for _, tc := range encodeCases {
		b.Run(tc.name, func(b *testing.B) {
			m := cover(tc.px)
			b.SetBytes(int64(len(m.Pix)))
			b.ReportAllocs()
			for b.Loop() {
				sink = tc.encode(m)
			}
		})
	}
}

// BenchmarkKittyCells makes a slot's placeholders, once per id and size.
func BenchmarkKittyCells(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		sink = KittyCells(176, 4, 2)
	}
}
