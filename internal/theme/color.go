package theme

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

// lch returns c's OKLCH lightness, chroma and hue, in Oklch's units:
// ui.Color keeps only sRGB.
func lch(c ui.Color) (l, ch, h float32) {
	r, g, b := linear(c.R), linear(c.G), linear(c.B)
	lm := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*b)
	mm := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*b)
	sm := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*b)
	L := 0.2104542553*lm + 0.7936177850*mm - 0.0040720468*sm
	A := 1.9779984951*lm - 2.4285922050*mm + 0.4505937099*sm
	B := 0.0259040371*lm + 0.7827717662*mm - 0.8086757660*sm
	hue := math.Atan2(B, A) * 180 / math.Pi
	if hue < 0 {
		hue += 360
	}
	return float32(L), float32(math.Hypot(A, B)), float32(hue)
}

// linear returns an sRGB channel's linear light.
func linear(v uint8) float64 {
	s := float64(v) / 255
	if s <= 0.04045 {
		return s / 12.92
	}
	return math.Pow((s+0.055)/1.055, 2.4)
}

// luminance is WCAG's relative luminance.
func luminance(c ui.Color) float64 {
	return 0.2126*linear(c.R) + 0.7152*linear(c.G) + 0.0722*linear(c.B)
}

// contrastRatio is the WCAG contrast ratio of two opaque colors, 1 to 21.
func contrastRatio(a, b ui.Color) float64 {
	la, lb := luminance(a), luminance(b)
	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}

// legible is WCAG AA's contrast for small text.
const legible = 4.5

// lighten raises fg's OKLCH lightness, keeping hue and chroma, until it
// keeps ratio on every bg; ok is false when even white cannot.
func lighten(fg ui.Color, ratio float64, bgs ...ui.Color) (c ui.Color, ok bool) {
	legibleOnAll := func(c ui.Color) bool {
		for _, bg := range bgs {
			if contrastRatio(c, bg) < ratio {
				return false
			}
		}
		return true
	}
	if legibleOnAll(fg) {
		return fg, true
	}
	l, ch, h := lch(fg)
	for ; l < 1; l += 0.005 {
		if c := Oklch(l, ch, h).Alpha(float32(fg.A) / 255); legibleOnAll(c) {
			return c, true
		}
	}
	return white, legibleOnAll(white)
}

// parseColor reads "#RGB", "#RRGGBB", "#RRGGBBAA", or "oklch(L C H)" with
// an optional "/ A" and L a number or percentage, clamped into sRGB.
func parseColor(s string) (ui.Color, error) {
	s = strings.TrimSpace(s)
	bad := func(why string) (ui.Color, error) {
		return ui.Color{}, fmt.Errorf("invalid color %q: %s", s, why)
	}
	if digits, ok := strings.CutPrefix(s, "#"); ok {
		// ui.Hex panics on malformed input.
		hasHexLen := len(digits) == 3 || len(digits) == 6 || len(digits) == 8
		if _, err := strconv.ParseUint(digits, 16, 32); err != nil || !hasHexLen {
			return bad("want #RGB, #RRGGBB or #RRGGBBAA")
		}
		return ui.Hex(s), nil
	}
	inner, isOklch := strings.CutPrefix(strings.ToLower(s), "oklch(")
	inner, closed := strings.CutSuffix(inner, ")")
	if !isOklch || !closed {
		return bad("want #RRGGBB or oklch(L C H)")
	}
	lchPart, alpha, hasAlpha := strings.Cut(inner, "/")
	fields := strings.Fields(lchPart)
	if len(fields) != 3 {
		return bad("oklch takes L, C and H")
	}
	var lchValues [3]float64
	for i, value := range fields {
		scale := 1.0
		if percent, ok := strings.CutSuffix(value, "%"); ok && i == 0 {
			value, scale = percent, 0.01
		}
		n, err := strconv.ParseFloat(value, 32)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return bad(fmt.Sprintf("%q is no number", value))
		}
		lchValues[i] = n * scale
	}
	l, ch := lchValues[0], lchValues[1]
	if l < 0 || l > 1 || ch < 0 || ch > 0.5 {
		return bad("L is 0 to 1 and C 0 to 0.5")
	}
	c := Oklch(float32(l), float32(ch), float32(lchValues[2]))
	if hasAlpha {
		a, err := strconv.ParseFloat(strings.TrimSpace(alpha), 32)
		if err != nil || a < 0 || a > 1 {
			return bad("alpha is 0 to 1")
		}
		c = c.Alpha(float32(a))
	}
	return c, nil
}

// formatColor writes c so parseColor reads it back exactly.
func formatColor(c ui.Color) string {
	if c.A == 255 {
		return Hex(c)
	}
	return fmt.Sprintf("#%02X%02X%02X%02X", c.R, c.G, c.B, c.A)
}
