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

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // Decode
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

// Decode decodes a JPEG, PNG or WebP image into a square cover of at most
// px pixels: it trims letterbox bars, crops the middle square, and scales
// down so a large header image does not stay in memory at full size.
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
	src, _, err := image.Decode(r)
	if err != nil {
		return nil, err
	}
	crop := square(trimBars(rgba64(src)))
	m := image.NewRGBA(image.Rect(0, 0, crop.Dx(), crop.Dx()))
	draw.Draw(m, m.Bounds(), src, crop.Min, draw.Src)
	m = shrink(m, px)
	return &Image{RGBA: m, Average: average(m)}, nil
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

// shrink scales a square image down to n pixels as MyGo draws a smaller
// bitmap: halving while at least twice as large, then bilinear, so every
// pixel counts. A kernel such as Catmull-Rom would allocate megabytes of
// float64s per image.
func shrink(m *image.RGBA, n int) *image.RGBA {
	for m.Rect.Dx() >= 2*n {
		m = halve(m)
	}
	if m.Rect.Dx() <= n {
		return m
	}
	dst := image.NewRGBA(image.Rect(0, 0, n, n))
	xdraw.ApproxBiLinear.Scale(dst, dst.Rect, m, m.Rect, draw.Src, nil)
	return dst
}

// halve averages each 2×2 block; an odd last row or column counts twice.
func halve(m *image.RGBA) *image.RGBA {
	w, h := m.Rect.Dx(), m.Rect.Dy()
	out := image.NewRGBA(image.Rect(0, 0, max(1, (w+1)/2), max(1, (h+1)/2)))
	for y := range out.Rect.Dy() {
		top := m.Pix[2*y*m.Stride:]
		bottom := m.Pix[min(2*y+1, h-1)*m.Stride:]
		outRow := out.Pix[y*out.Stride:]
		for x := range out.Rect.Dx() {
			left, right := 8*x, 4*min(2*x+1, w-1)
			for c := range 4 {
				sum := uint32(top[left+c]) + uint32(top[right+c]) + uint32(bottom[left+c]) + uint32(bottom[right+c])
				outRow[4*x+c] = uint8((sum + 2) / 4) //nolint:gosec // the mean of four bytes
			}
		}
	}
	return out
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
