package main

import (
	"hash/fnv"
	"image"
	"math"
	"strings"
	"yt-music-player/internal/artwork"
	"yt-music-player/internal/catalog"

	"github.com/egoist/mygo/ui"
)

type motif uint8

const (
	motifRings motif = iota
	motifSun
	motifDots
	motifWaves
	motifArches
	motifStripes
)

// cover is generated album art: the placeholder releases' own, and a
// thumbnail's while it loads or when it fails. Painted, it keeps the app
// asset-free and gives every release a distinct color for the glow.
type cover struct {
	from, to, ink ui.Color
	motif         motif
}

const artLiked catalog.Art = "liked"

var covers = map[catalog.Art]cover{
	"neon-tide":     {oklch(0.62, 0.24, 350), oklch(0.3, 0.16, 285), ui.Hex("#FFD6F0"), motifRings},
	"paper-moons":   {oklch(0.8, 0.15, 75), oklch(0.48, 0.17, 35), ui.Hex("#FFF4D6"), motifSun},
	"low-orbit":     {oklch(0.6, 0.11, 195), oklch(0.26, 0.08, 250), ui.Hex("#D8FBFF"), motifDots},
	"velvet-static": {oklch(0.5, 0.2, 20), oklch(0.22, 0.1, 330), ui.Hex("#FFC9C2"), motifWaves},
	"glasshouse":    {oklch(0.78, 0.13, 160), oklch(0.35, 0.08, 165), ui.Hex("#EFFFF6"), motifArches},
	"afterglow-fm":  {oklch(0.82, 0.1, 50), oklch(0.62, 0.19, 25), ui.Hex("#FFF0E6"), motifStripes},
	"northbound":    {oklch(0.78, 0.07, 235), oklch(0.36, 0.05, 260), ui.Hex("#F0F6FF"), motifRings},
	"saffron-hours": {oklch(0.82, 0.16, 95), oklch(0.45, 0.1, 120), ui.Hex("#FFFBE0"), motifSun},
	"small-hours":   {oklch(0.7, 0.13, 305), oklch(0.25, 0.12, 290), ui.Hex("#F3E6FF"), motifWaves},
	"copper-lines":  {oklch(0.68, 0.16, 50), oklch(0.3, 0.07, 40), ui.Hex("#FFE3CC"), motifStripes},
	"blue-cassette": {oklch(0.7, 0.14, 225), oklch(0.3, 0.14, 265), ui.Hex("#DDF3FF"), motifArches},
	"ember-ash":     {oklch(0.55, 0.2, 30), oklch(0.16, 0.02, 30), ui.Hex("#FFB8A8"), motifDots},
	artLiked:        {oklch(0.68, 0.2, 33), oklch(0.42, 0.2, 350), coverWhite, motifStripes},
}

// Covers are content: their colors ignore the theme.
var (
	coverWhite = ui.RGB(255, 255, 255)
	coverBlack = ui.RGB(0, 0, 0)
)

// coverOf returns the cover for art: a loaded thumbnail's average color,
// a loading one's colors from its URL, or a plain cover for other art.
func coverOf(art catalog.Art) cover {
	if cv, ok := covers[art]; ok {
		return cv
	}
	if !isURL(art) {
		col := &th.Color
		return cover{from: col.CoverFrom, to: col.CoverTo, ink: col.CoverInk, motif: motifRings}
	}
	if thumbs != nil {
		if avg, ok := thumbs.Average(string(art)); ok {
			from := ui.RGB(avg.R, avg.G, avg.B)
			return cover{from: from, to: from.Mix(coverBlack, 0.6), ink: coverWhite, motif: motifDots}
		}
	}
	return urlCover(art)
}

var urlCovers = map[catalog.Art]cover{}

// urlCover picks a hue and motif from art's hash, so loading covers
// differ as the releases do. Its motifs need no clip (see cover.clips).
func urlCover(art catalog.Art) cover {
	if cv, ok := urlCovers[art]; ok {
		return cv
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(art))
	sum := hash.Sum32()
	hue := float32(sum % 360)
	pattern := motifDots
	if sum&(1<<12) != 0 {
		pattern = motifStripes
	}
	cv := cover{from: oklch(0.6, 0.12, hue), to: oklch(0.27, 0.07, hue+40), ink: oklch(0.93, 0.04, hue), motif: pattern}
	// A session sees thousands of songs at most; clearing bounds the map.
	if len(urlCovers) >= 4096 {
		clear(urlCovers)
	}
	urlCovers[art] = cv
	return cv
}

func isURL(art catalog.Art) bool {
	return strings.HasPrefix(string(art), "https://") || strings.HasPrefix(string(art), "http://")
}

// artScale fetches thumbnails at twice their shown size, sharp on Retina.
// stretchedCover is the assumed width, in DIPs, of a stretched cover not
// laid out yet.
const (
	artScale       = 2
	stretchedCover = 200
)

// artCacheDir keeps thumbnails between runs; tests set it to "".
var artCacheDir = func() string {
	dir, _ := artwork.DefaultDir()
	return dir
}()

// thumbs is the loader of the app being built. A global, because covers
// are built by free functions all over the view; UI thread only.
var thumbs *artwork.Loader

// useArt makes a.art the loader covers use, and counts the build as a
// frame for its memory budget. The loader starts lazily: an app no window
// shows needs none.
func (a *app) useArt() {
	if a.art == nil {
		a.art = artwork.New(artwork.Options{
			Post:    a.post,
			Prepare: func(m *image.RGBA) any { return ui.NewBitmap(m) },
			Dir:     artCacheDir,
		})
	}
	thumbs = a.art
	a.art.NextFrame()
}

// thumbnail returns art's bitmap for box, a cover w DIPs wide (0 if
// stretched), or nil while it loads.
func thumbnail(c *ui.Context, box ui.Element, art catalog.Art, w float32) *ui.Bitmap {
	if thumbs == nil || !isURL(art) {
		return nil
	}
	if w <= 0 {
		w = stretchedWidth(c, box, art)
	}
	if w <= 0 {
		return nil
	}
	img := thumbs.Get(string(art), int(math.Ceil(float64(w*artScale))))
	if img == nil {
		return nil
	}
	bitmap, _ := img.UI.(*ui.Bitmap)
	return bitmap
}

// stretchedWidths are the widths each art's stretched cover was last laid
// out at, 0 while the first is measured.
var stretchedWidths = map[catalog.Art]float32{}

// stretchedWidth returns box's width in the last frame, else that of
// art's last stretched cover, as on a page shown again. Grid cells are
// 150 to 340 DIPs wide, so a guess would fetch twice or blur: a new cover
// asks for one more frame to measure it and returns 0 meanwhile. Only
// once per art, so a cover never laid out can't draw frames forever.
func stretchedWidth(c *ui.Context, box ui.Element, art catalog.Art) float32 {
	if w := box.LayoutBounds().W; w > 0 {
		// Clearing bounds the map in a long session.
		if len(stretchedWidths) >= 4096 {
			clear(stretchedWidths)
		}
		stretchedWidths[art] = w
		return w
	}
	w, measuring := stretchedWidths[art]
	switch {
	case !measuring:
		stretchedWidths[art] = 0
		c.Invalidate()
		return 0
	case w == 0:
		return stretchedCover
	}
	return w
}

// coverArt's width 0 makes it a square stretched across its parent.
func coverArt(c *ui.Context, art catalog.Art, w, h, radius float32) ui.Element {
	box := ui.Box(c).Shrink(0).Radius(radius)
	if w > 0 {
		box.Size(w, h)
	} else {
		box.AspectRatio(1)
	}
	bitmap := thumbnail(c, box, art, w)
	cv := coverOf(art)
	if bitmap != nil {
		// The image rounds its own corners in its shader, even turned: a
		// rounded clip would cost a mask texture under a transform. The
		// average color behind it blends its antialiased edge.
		box.Background(cv.from).Children(func() {
			ui.Image(c, bitmap).Absolute().Top(0).Right(0).Bottom(0).Left(0).Fit(ui.Cover).Radius(radius)
		})
		return box
	}
	box.LinearGradient(ui.LinearGradient{From: cv.from, To: cv.to, Angle: 145, Oklab: true}).
		Draw(cv.painter(radius))
	if cv.motif == motifStripes {
		// The GPU shades stripes as a fill: no path, no clip.
		side := w
		if side <= 0 {
			side = stretchedCover
		}
		box.Children(func() {
			ui.Box(c).Absolute().Top(0).Right(0).Bottom(0).Left(0).Radius(radius).
				Stripes(cv.ink.Alpha(0.26), side*0.09, side*0.145, 26.6)
		})
	}
	return box
}

type painterKey struct {
	cv     cover
	radius float32
}

// painters caches covers' draw functions: a closure per cover per build
// would allocate.
var painters = map[painterKey]func(*ui.Painter, ui.Rect){}

func (cv cover) painter(radius float32) func(*ui.Painter, ui.Rect) {
	key := painterKey{cv: cv, radius: radius}
	if paint, ok := painters[key]; ok {
		return paint
	}
	paint := func(p *ui.Painter, r ui.Rect) { cv.paint(p, r, min(radius, r.W/2)) }
	// Covers are as many as thumbnails; clearing bounds the map.
	if len(painters) >= 4096 {
		clear(painters)
	}
	painters[key] = paint
	return paint
}

// paint draws the motif with shapes the GPU shades directly, at any size,
// in no memory. Paths would be rasterized into the shared mask atlas for
// every size and subpixel offset, growing it to 16 MB plus as much again
// for its texture; bitmaps of the covers would take more.
func (cv cover) paint(p *ui.Painter, r ui.Rect, radius float32) {
	side := min(r.W, r.H)
	if cv.clips(radius, side) {
		p.Clip(r, radius, func() { cv.motifs(p, r, side) })
	} else {
		cv.motifs(p, r, side)
	}
	// A soft top-left sheen makes flat gradients read as printed sleeves.
	sheen := ui.LinearGradient{From: coverWhite.Alpha(0.14), To: coverWhite.Alpha(0), Angle: 135, End: 0.55}
	p.FillGradient(r, sheen, radius)
}

// clips reports whether cv, painted side DIPs square and rounded by
// radius, clips its motif. Only a cover that doesn't may be posed or
// transformed: a moved clip is drawn through a mask texture. Dots stay
// inside a square cover; stripes are shaded as a fill. A shown thumbnail
// clips nothing, so for one this errs toward true.
func (cv cover) clips(radius, side float32) bool {
	dotsInside := cv.motif == motifDots && min(radius, side/2) < side*0.2
	return !dotsInside && cv.motif != motifStripes
}

func (cv cover) motifs(p *ui.Painter, r ui.Rect, side float32) {
	ink := cv.ink
	switch cv.motif {
	case motifRings:
		cx, cy := r.X+r.W*0.7, r.Y+r.H*0.32
		for i := 6; i >= 1; i-- {
			ring(p, cx, cy, side*0.09*float32(i), max(side*0.016, 0.8), ink.Alpha(0.85-float32(i)*0.11))
		}
		disc(p, cx, cy, side*0.05, ink)
	case motifSun:
		cx, cy, rad := r.X+r.W*0.5, r.Y+r.H*0.6, side*0.27
		sun := ui.Rect{X: cx - rad, Y: cy - rad, W: 2 * rad, H: 2 * rad}
		p.FillGradient(sun, ui.LinearGradient{From: ink, To: ink.Alpha(0.55), Angle: 180}, rad)
		// Retro horizon bands cut the lower half of the sun.
		for i := range 5 {
			fi := float32(i)
			y := cy + rad*0.1 + fi*rad*0.2
			p.Fill(ui.Rect{X: r.X, Y: y, W: r.W, H: rad * (0.04 + fi*0.025)}, cv.to, 0)
		}
		p.Fill(ui.Rect{X: r.X, Y: cy + rad, W: r.W, H: r.H}, cv.to.Alpha(0.6), 0)
	case motifDots:
		n := 6
		gap := r.W / float32(n+1)
		for i := 1; i <= n; i++ {
			for j := 1; j <= n; j++ {
				k := float32((i+j)%4) * 0.006
				x, y := r.X+gap*float32(i), r.Y+gap*float32(j)*r.H/r.W
				disc(p, x, y, side*(0.018+k), ink.Alpha(0.35+float32((i*j)%5)*0.1))
			}
		}
		disc(p, r.X+r.W*0.32, r.Y+r.H*0.68, side*0.2, ink.Alpha(0.9))
	case motifWaves:
		for k := range 7 {
			fk := float32(k)
			wave(p, r, r.Y+r.H*(0.28+fk*0.09), side*(0.03+fk*0.008), r.W*2/3, fk*0.6/(2*math.Pi),
				max(side*0.014, 0.8), ink.Alpha(0.25+fk*0.1))
		}
	case motifArches:
		cx, by := r.X+r.W*0.5, r.Y+r.H
		for i := range 4 {
			rad := side * (0.62 - float32(i)*0.14)
			// A pill standing past the bottom edge: the clip leaves its
			// rounded top as the arch.
			arch := ui.Rect{X: cx - rad, Y: by - rad*1.6, W: 2 * rad, H: rad * 2.6}
			p.Fill(arch, ink.Alpha(0.25+float32(i)*0.22), rad)
		}
	}
}

func disc(p *ui.Painter, cx, cy, rad float32, c ui.Color) {
	p.Fill(ui.Rect{X: cx - rad, Y: cy - rad, W: 2 * rad, H: 2 * rad}, c, rad)
}

// ring's stroke is centered on rad, as a stroked circle path's would be.
func ring(p *ui.Painter, cx, cy, rad, width float32, c ui.Color) {
	outer := rad + width/2
	p.Stroke(ui.Rect{X: cx - outer, Y: cy - outer, W: 2 * outer, H: 2 * outer}, c, outer, width)
}

// wave strokes a sine-like line across r as circular arcs, one per half
// period, each cut from a ring by a clip. Mirrored arcs share tangents
// where they meet, so the line stays smooth. phase is in periods.
func wave(p *ui.Painter, r ui.Rect, base, amp, period, phase, width float32, c ui.Color) {
	half := period / 2
	// The circle through both ends of a half period and its crest.
	rad := (half*half/4 + amp*amp) / (2 * amp)
	// How far past the chord the stroke may reach before the clip lets in
	// the rest of the ring.
	under := min(width/2, rad-amp)
	x := r.X - (phase-float32(math.Floor(float64(phase))))*period
	for up := false; x < r.X+r.W; x, up = x+half, !up {
		clip := ui.Rect{X: x, Y: base - amp - width, W: half, H: amp + width + under}
		cy := base - amp + rad
		if !up {
			clip.Y = base - under
			cy = base + amp - rad
		}
		p.Clip(clip, 0, func() { ring(p, x+half/2, cy, rad, width, c) })
	}
}
