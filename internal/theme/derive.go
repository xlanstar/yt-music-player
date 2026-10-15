package theme

import "github.com/egoist/mygo/ui"

// Seed is what Derive makes a theme from. A zero color takes Default's.
type Seed struct {
	Accent ui.Color
	// Base is the window's background; its hue and chroma tint every
	// surface.
	Base ui.Color
	// A zero Text derives a near white of Base's hue.
	Text ui.Color
}

// Derive's bounds on seeds, in OKLCH.
const (
	// maxBaseL keeps the player dark whatever the seed: art carries the
	// color, and the white washes and light text over art would wash out
	// on a light base. A lighter base keeps its hue, darkened.
	maxBaseL = 0.24
	// minBaseL keeps surface steps apart: below it they round to the same
	// 8-bit black.
	minBaseL = 0.1
	// maxSurfaceC keeps surfaces calm beside the art.
	maxSurfaceC = 0.04
	// The accent stays light enough to read on the surfaces, and dark
	// enough that white reads on it up to whiteOnAccentL.
	minAccentL, maxAccentL = 0.6, 0.85
	whiteOnAccentL         = 0.72
)

// Derive returns a whole theme from seed: surfaces stepping up in
// lightness from the base, text keeping 4.5:1 on every surface, the
// accent's states, and Default's washes, scales and layout, all inside
// sRGB. Default itself is tuned by hand.
func Derive(seed Seed) Theme {
	t := Default()
	if seed.Accent.A == 0 {
		seed.Accent = t.Color.Accent
	}
	if seed.Base.A == 0 {
		seed.Base = t.Color.Base
	}
	baseL, baseC, baseH := lch(seed.Base)
	baseL, baseC = min(max(baseL, minBaseL), maxBaseL), min(baseC, maxSurfaceC)
	surface := func(step float32) ui.Color { return Oklch(baseL+step, baseC, baseH) }
	accentL, accentC, accentH := lch(seed.Accent)
	accentL = min(max(accentL, minAccentL), maxAccentL)
	accent := Oklch(accentL, accentC, accentH)

	c := &t.Color
	c.Base, c.Panel, c.Raised = surface(0), surface(0.047), surface(0.087)
	c.Hover, c.Pressed = surface(0.126), surface(0.171)
	c.Inverse = surface(0.15)
	// The quieter shades must read on the inverse too, as toasts' lines.
	textBackgrounds := [...]ui.Color{c.Base, c.Panel, c.Raised, c.Inverse}
	surfaces := textBackgrounds[:3]
	text := seed.Text
	if text.A == 0 {
		text = Oklch(0.96, min(baseC, 0.01), baseH)
	}
	c.Text, _ = lighten(text.Alpha(1), 7, surfaces...)
	grayC := min(baseC, 0.015)
	c.Faint, _ = lighten(Oklch(0.64, grayC, baseH), legible, textBackgrounds[:]...)
	faintL, _, _ := lch(c.Faint)
	c.Muted, _ = lighten(Oklch(max(0.7, faintL+0.06), grayC, baseH), legible, textBackgrounds[:]...)
	c.InverseText = c.Text

	// The accent tints icons on the surfaces: 3:1, as for graphics.
	c.Accent, _ = lighten(accent, 3, surfaces...)
	accentL, accentC, accentH = lch(c.Accent)
	c.AccentHover = c.Accent.Mix(white, 0.1)
	c.AccentPressed = c.Accent.Mix(black, 0.1)
	c.AccentText = white
	if accentL > whiteOnAccentL {
		c.AccentText = c.Base
	}
	c.Focus = c.Accent.Alpha(0.8)
	c.Selection = c.Accent.Alpha(0.35)
	c.Charts = c.Accent
	c.Brand = Oklch(accentL-0.18, min(accentC+0.02, 0.3), accentH-23)
	c.CoverFrom, c.CoverTo, c.CoverInk = surface(0.21), c.Raised, c.Muted

	hc := &t.Contrast
	hc.Focus = c.Accent
	hc.Faint, _ = lighten(Oklch(max(0.76, faintL+0.1), grayC, baseH), legible, surfaces...)
	hc.Muted, _ = lighten(Oklch(0.86, grayC, baseH), legible, surfaces...)
	return t.SRGB()
}
