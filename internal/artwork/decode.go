package artwork

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg" // Decode
	_ "image/png"  // Decode
	"io"

	_ "golang.org/x/image/webp" // Decode: SizedURL asks Google's hosts for it
)

// maxPixels keeps a corrupt or hostile header from allocating gigabytes.
const maxPixels = 4096 * 4096

// Image is a decoded cover: square, at most the size it was asked for.
type Image struct {
	// RGBA holds premultiplied pixels from the origin, so a toolkit takes
	// them without a copy. Read only.
	RGBA *image.RGBA
	// Average is the opaque average color.
	Average color.RGBA
	// UI is what Options.Prepare made of RGBA.
	UI any
}

// Bytes returns the memory the image's pixels take.
func (m *Image) Bytes() int { return len(m.RGBA.Pix) }

// Decode decodes a WebP, JPEG or PNG image into a square cover of at most
// px pixels: it trims letterbox bars, crops the middle
// square, and scales down so a large image does not stay in memory.
func Decode(data []byte, px int) (*Image, error) {
	if px <= 0 {
		return nil, errors.New("artwork: no size")
	}
	r := &peekReader{data}
	cfg, _, err := image.DecodeConfig(r)
	if err != nil {
		return nil, err
	}
	isEmpty := cfg.Width <= 0 || cfg.Height <= 0
	if isEmpty || cfg.Width*cfg.Height > maxPixels {
		return nil, fmt.Errorf("artwork: image of %d×%d pixels", cfg.Width, cfg.Height)
	}
	r.data = data
	src, format, err := image.Decode(r)
	if err != nil {
		return nil, err
	}
	if format == "webp" {
		fullRange(src)
	}
	crop := square(trimBars(rgba64(src)))
	var m *image.RGBA
	if side := crop.Dx(); side > px {
		m = shrink(src, crop, px)
	} else {
		m = image.NewRGBA(image.Rect(0, 0, side, side))
		draw.Draw(m, m.Rect, src, crop.Min, draw.Src)
	}
	return &Image{RGBA: m, Average: average(m)}, nil
}

// fullY and fullC map VP8's limited range (Y 16–235, Cb and Cr 16–240)
// to full, rounded.
var (
	fullY = rangeTable(16, 235)
	fullC = rangeTable(16, 240)
)

func rangeTable(lo, hi int) (t [256]uint8) {
	for v := range t {
		x := ((v-lo)*255*2 + hi - lo) / (2 * (hi - lo))
		t[v] = uint8(min(max(x, 0), 255))
	}
	return t
}

// fullRange expands a lossy WebP's planes in place: x/image/webp returns
// VP8's limited-range YCbCr, which Go converts as JPEG's full range, ~8
// levels washed out. Expanded, Go's conversion is libwebp's.
func fullRange(src image.Image) {
	var m *image.YCbCr
	switch src := src.(type) {
	case *image.YCbCr:
		m = src
	case *image.NYCbCrA:
		m = &src.YCbCr
	default:
		return // lossless: RGB already
	}
	for i, v := range m.Y {
		m.Y[i] = fullY[v]
	}
	for i, v := range m.Cb {
		m.Cb[i] = fullC[v]
	}
	for i, v := range m.Cr {
		m.Cr[i] = fullC[v]
	}
}

// peekReader reads bytes with the Peek image.Decode looks for: without it,
// DecodeConfig and Decode each wrap the reader in a 4 KB bufio.Reader.
type peekReader struct{ data []byte }

func (r *peekReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

// Peek returns the next n bytes, fewer and io.EOF at the end.
func (r *peekReader) Peek(n int) ([]byte, error) {
	if n > len(r.data) {
		return r.data, io.EOF
	}
	return r.data[:n], nil
}

// shrink returns the square r of src scaled down to n pixels, each the
// mean of the area it covers: a box filter, which sees every pixel in one
// pass. It reads src a row at a time: a copy of it whole took megabytes.
func shrink(src image.Image, r image.Rectangle, n int) *image.RGBA {
	side := r.Dx()
	dst := image.NewRGBA(image.Rect(0, 0, n, n))
	rgba, isRGBA := src.(*image.RGBA)
	var buf *image.RGBA
	if !isRGBA {
		buf = image.NewRGBA(image.Rect(0, 0, side, 1))
	}
	// row sums source row j, horizontally, into sums.
	row := func(sums []uint32, j int, spans []span) {
		if isRGBA {
			sumRow(sums, rgba.Pix[rgba.PixOffset(r.Min.X, r.Min.Y+j):][:4*side], spans)
			return
		}
		draw.Draw(buf, buf.Rect, src, image.Pt(r.Min.X, r.Min.Y+j), draw.Src)
		sumRow(sums, buf.Pix, spans)
	}
	spans := boxSpans(side, n)
	// An output row's first source row, those between, and its last,
	// which is often the next one's first.
	sums := make([]uint32, 12*n)
	first, mid, last := sums[:4*n], sums[4*n:8*n], sums[8*n:]
	whole, area := uint64(n), uint64(side*side) //nolint:gosec // sizes
	prev := -1
	for y, sp := range spans {
		if sp.first == prev {
			first, last = last, first
		} else {
			clear(first)
			row(first, sp.first, spans)
		}
		clear(mid)
		for j := sp.first + 1; j < sp.last; j++ {
			row(mid, j, spans)
		}
		clear(last)
		row(last, sp.last, spans)
		prev = sp.last
		out := dst.Pix[y*dst.Stride : (y+1)*dst.Stride]
		for i := range out {
			v := uint64(sp.firstPart)*uint64(first[i]) + whole*uint64(mid[i]) + uint64(sp.lastPart)*uint64(last[i])
			out[i] = uint8((v + area/2) / area) //nolint:gosec // a mean of bytes
		}
	}
	return dst
}

// span is the source pixels an output pixel covers: in units of 1/n of
// one, the first and last in part, those between whole.
type span struct {
	first, last         int
	firstPart, lastPart uint32
}

// boxSpans returns the spans of n pixels over side > n: pixel x covers
// [x*side, (x+1)*side) in units of 1/n, so it crosses a source pixel's edge.
func boxSpans(side, n int) []span {
	spans := make([]span, n)
	for x := range spans {
		lo, hi := x*side, (x+1)*side
		first, last := lo/n, (hi-1)/n
		spans[x] = span{
			first: first, last: last,
			firstPart: uint32((first+1)*n - lo), //nolint:gosec // at most n
			lastPart:  uint32(hi - last*n),      //nolint:gosec // at most n
		}
	}
	return spans
}

// sumRow adds a row of pixels to sums, one per span, weighed in units of
// 1/n: at most 255×side a row, and side ≤ 4096 (maxPixels) keeps a sum of
// rows in uint32.
func sumRow(sums []uint32, pix []uint8, spans []span) {
	n := uint32(len(spans)) //nolint:gosec // a size
	for x, sp := range spans {
		var r, g, b, a uint32
		for i := 4 * (sp.first + 1); i < 4*sp.last; i += 4 {
			p := pix[i : i+4 : i+4]
			r += uint32(p[0])
			g += uint32(p[1])
			b += uint32(p[2])
			a += uint32(p[3])
		}
		f := pix[4*sp.first : 4*sp.first+4 : 4*sp.first+4]
		l := pix[4*sp.last : 4*sp.last+4 : 4*sp.last+4]
		s := sums[4*x : 4*x+4 : 4*x+4]
		s[0] += n*r + sp.firstPart*uint32(f[0]) + sp.lastPart*uint32(l[0])
		s[1] += n*g + sp.firstPart*uint32(f[1]) + sp.lastPart*uint32(l[1])
		s[2] += n*b + sp.firstPart*uint32(f[2]) + sp.lastPart*uint32(l[2])
		s[3] += n*a + sp.firstPart*uint32(f[3]) + sp.lastPart*uint32(l[3])
	}
}

func square(r image.Rectangle) image.Rectangle {
	w, h := r.Dx(), r.Dy()
	side := min(w, h)
	x, y := r.Min.X+(w-side)/2, r.Min.Y+(h-side)/2
	return image.Rect(x, y, x+side, y+side)
}

// rgba64 returns src with RGBA64At, which returns the color unboxed: At
// allocates one per pixel. Every decoder's image has it. It is asserted
// once per image, not per pixel: now and then an assertion allocates (the
// runtime building its assertion cache).
func rgba64(src image.Image) image.RGBA64Image {
	if m, ok := src.(image.RGBA64Image); ok {
		return m
	}
	return atRGBA64{src}
}

// atRGBA64 reads an image without RGBA64At through At.
type atRGBA64 struct{ image.Image }

func (m atRGBA64) RGBA64At(x, y int) color.RGBA64 {
	r, g, b, a := m.At(x, y).RGBA()
	return color.RGBA64{uint16(r), uint16(g), uint16(b), uint16(a)} //nolint:gosec // RGBA's channels are 16-bit
}

// trimBars drops the black bars of a 4:3 still of a 16:9 video, as
// YouTube's hqdefault and sddefault are.
func trimBars(src image.RGBA64Image) image.Rectangle {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w*3 != h*4 || h < 16 {
		return b
	}
	bar := h / 8
	// JPEG's subsampled chroma bleeds the picture into the rows next to
	// it, so only rows further in are tested.
	for _, y := range [...]int{0, bar / 2, bar * 3 / 4, h - 1 - bar*3/4, h - 1 - bar/2, h - 1} {
		for x := 0; x < w; x += max(1, w/32) {
			c := src.RGBA64At(b.Min.X+x, b.Min.Y+y)
			// Near black: JPEG leaves the bars a few levels above zero.
			if c.R > 0x1800 || c.G > 0x1800 || c.B > 0x1800 {
				return b
			}
		}
	}
	return image.Rect(b.Min.X, b.Min.Y+bar, b.Max.X, b.Max.Y-bar)
}

// average returns the mean of premultiplied pixels as an opaque,
// unpremultiplied color.
func average(m *image.RGBA) color.RGBA {
	var r, g, b, a uint64
	for i := 0; i+3 < len(m.Pix); i += 4 {
		p := m.Pix[i : i+4 : i+4]
		r += uint64(p[0])
		g += uint64(p[1])
		b += uint64(p[2])
		a += uint64(p[3])
	}
	if a == 0 {
		return color.RGBA{A: 255}
	}
	// Dividing premultiplied sums by the alpha sum weighs pixels by opacity.
	// Premultiplied, no channel's sum passes the alpha sum, so each fits a byte.
	return color.RGBA{uint8(r * 255 / a), uint8(g * 255 / a), uint8(b * 255 / a), 255} //nolint:gosec // see above
}
