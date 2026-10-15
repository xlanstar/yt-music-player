package ytmusic

import (
	"math"
	"yt-music-player/internal/catalog"
)

// Bounds of a mood's tint. YouTube Music's stripe colors suit a light
// theme too, so near-black or neon ones would vanish or glare on the
// always-dark UI; these keep the placeholder's range.
const (
	minTintL = 0.55
	maxTintL = 0.85
	maxTintC = 0.2
)

// tint ignores alpha: stripes are opaque.
func tint(argb uint32) catalog.Tint {
	l, c, h := oklch(argb)
	return catalog.Tint{
		L: float32(min(max(l, minTintL), maxTintL)),
		C: float32(min(c, maxTintC)),
		H: float32(h),
	}
}

// oklch converts an sRGB color to OKLCH: lightness 0–1, chroma, and hue
// in degrees in [0, 360). It follows Björn Ottosson's OKLab definition.
func oklch(argb uint32) (l, c, h float64) {
	r := linear(argb >> 16 & 0xff)
	g := linear(argb >> 8 & 0xff)
	b := linear(argb & 0xff)

	lc := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*b)
	mc := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*b)
	sc := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*b)

	l = 0.2104542553*lc + 0.7936177850*mc - 0.0040720468*sc
	a := 1.9779984951*lc - 2.4285922050*mc + 0.4505937099*sc
	bb := 0.0259040371*lc + 0.7827717662*mc - 0.8086757660*sc

	c = math.Hypot(a, bb)
	// A gray's hue is rounding noise; 0 keeps equal grays equal.
	if c < 1e-4 {
		return l, 0, 0
	}
	h = math.Mod(math.Atan2(bb, a)*180/math.Pi+360, 360)
	return l, c, h
}

// linear decodes an sRGB channel's transfer curve.
func linear(v uint32) float64 {
	x := float64(v) / 255
	if x <= 0.04045 {
		return x / 12.92
	}
	return math.Pow((x+0.055)/1.055, 2.4)
}
