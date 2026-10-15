package theme

import (
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
)

var (
	sinkTheme  Theme
	sinkLayout Layout
	sinkSizes  Sizes
)

// The app calls these once per theme chosen, never per frame.

func BenchmarkDefault(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		sinkTheme = Default()
	}
}

func BenchmarkHighContrast(b *testing.B) {
	t := Default()
	b.ReportAllocs()
	for b.Loop() {
		sinkTheme = t.HighContrast()
	}
}

func BenchmarkSRGB(b *testing.B) {
	t := Default()
	b.ReportAllocs()
	for b.Loop() {
		sinkTheme = t.SRGB()
	}
}

func BenchmarkNormalized(b *testing.B) {
	for name, side := range map[string]Side{"Normal": Right, "SameSide": Left} {
		b.Run(name, func(b *testing.B) {
			l := Default().Layout
			l.PanelSide = side
			b.ReportAllocs()
			for b.Loop() {
				sinkLayout = l.Normalized()
			}
		})
	}
}

// Sized runs in every song row's build.
func BenchmarkSized(b *testing.B) {
	for name, d := range map[string]Density{"Comfortable": Comfortable, "Compact": Compact} {
		b.Run(name, func(b *testing.B) {
			l := Default().Layout
			l.Density = d
			b.ReportAllocs()
			for b.Loop() {
				sinkSizes = l.Sized()
			}
		})
	}
}

var (
	sinkName  string
	sinkBytes []byte
	sinkColor ui.Color
)

func BenchmarkDerive(b *testing.B) {
	seed := Seed{Accent: Oklch(0.72, 0.14, 250), Base: Oklch(0.16, 0.03, 265)}
	b.ReportAllocs()
	for b.Loop() {
		sinkTheme = Derive(seed)
	}
}

// example is the theme file the docs show.
func example(tb testing.TB) []byte {
	tb.Helper()
	data, err := os.ReadFile("testdata/dusk.json")
	if err != nil {
		tb.Fatal(err)
	}
	return data
}

// encoded has every token: the largest file a user writes.
func encoded(tb testing.TB) []byte {
	tb.Helper()
	data, err := Encode("Default", Default())
	if err != nil {
		tb.Fatal(err)
	}
	return data
}

func BenchmarkParse(b *testing.B) {
	for name, data := range map[string][]byte{"Example": example(b), "Full": encoded(b)} {
		b.Run(name, func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			for b.Loop() {
				sinkName, sinkTheme, _ = Parse(data)
			}
		})
	}
}

func BenchmarkEncode(b *testing.B) {
	t := Default()
	b.ReportAllocs()
	for b.Loop() {
		sinkBytes, _ = Encode("Default", t)
	}
}

func BenchmarkParseColor(b *testing.B) {
	for name, s := range map[string]string{"Hex": "#7AA2F7", "Oklch": "oklch(0.17 0.03 275 / 0.5)"} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkColor, _ = parseColor(s)
			}
		})
	}
}
