package theme

import (
	"testing"
	"yt-music-player/internal/budget"
)

func TestBudgetDefault(t *testing.T) {
	// 0 allocs today.
	budget.Allocs(t, 0, 100, func() { sinkTheme = Default() })
}

func TestBudgetHighContrast(t *testing.T) {
	th := Default()
	// 0 allocs today.
	budget.Allocs(t, 0, 100, func() { sinkTheme = th.HighContrast() })
}

func TestBudgetSRGB(t *testing.T) {
	th := Default()
	// 0 allocs today.
	budget.Allocs(t, 0, 100, func() { sinkTheme = th.SRGB() })
}

func TestBudgetNormalized(t *testing.T) {
	l := Default().Layout
	l.PanelSide = l.SidebarSide
	// 0 allocs today.
	budget.Allocs(t, 0, 100, func() { sinkLayout = l.Normalized() })
}

func TestBudgetSized(t *testing.T) {
	l := Default().Layout
	l.Density = Compact
	// 0 allocs today.
	budget.Allocs(t, 0, 100, func() { sinkSizes = l.Sized() })
}

func TestBudgetDerive(t *testing.T) {
	seed := Seed{Accent: Oklch(0.72, 0.14, 250), Base: Oklch(0.16, 0.03, 265)}
	// 0 allocs today.
	budget.Allocs(t, 0, 100, func() { sinkTheme = Derive(seed) })
}

func TestBudgetParse(t *testing.T) {
	ex, full := example(t), encoded(t)
	// 43 allocs today: the decoder's maps and the seed's colors.
	budget.Allocs(t, 50, 100, func() { sinkName, sinkTheme, _ = Parse(ex) })
	// 162 allocs today: every token.
	budget.Allocs(t, 187, 50, func() { sinkName, sinkTheme, _ = Parse(full) })
}

func TestBudgetEncode(t *testing.T) {
	th := Default()
	// 363 allocs today.
	budget.Allocs(t, 418, 50, func() { sinkBytes, _ = Encode("Default", th) })
}

func TestBudgetParseColor(t *testing.T) {
	// 0 allocs today.
	budget.Allocs(t, 0, 100, func() { sinkColor, _ = parseColor("#7AA2F7") })
	// 1 alloc today: the lowered copy.
	budget.Allocs(t, 1, 100, func() { sinkColor, _ = parseColor("oklch(0.17 0.03 275 / 0.5)") })
}
