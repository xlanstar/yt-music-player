package artwork

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	xdraw "golang.org/x/image/draw"
)

// solid returns a w×h image of c whose top and bottom h/8 rows are band,
// unless band is zero.
func solid(w, h int, c, band color.RGBA) *image.RGBA {
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		row := c
		if band != (color.RGBA{}) && (y < h/8 || y >= h-h/8) {
			row = band
		}
		for x := range w {
			m.SetRGBA(x, y, row)
		}
	}
	return m
}

func encodePNG(tb testing.TB, m image.Image) []byte {
	tb.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, m); err != nil {
		tb.Fatal(err)
	}
	return b.Bytes()
}

func encodeJPEG(tb testing.TB, m image.Image) []byte {
	tb.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, m, &jpeg.Options{Quality: 95}); err != nil {
		tb.Fatal(err)
	}
	return b.Bytes()
}

func near(a, b color.RGBA, tol int) bool {
	within := func(x, y uint8) bool { return int(x)-int(y) <= tol && int(y)-int(x) <= tol }
	return within(a.R, b.R) && within(a.G, b.G) && within(a.B, b.B) && within(a.A, b.A)
}

func TestDecodeScalesDownToSquare(t *testing.T) {
	red := color.RGBA{200, 40, 40, 255}
	// A 1200×900 header, as a playlist's, shown at 256.
	for name, data := range map[string][]byte{
		"png":  encodePNG(t, solid(1200, 900, red, color.RGBA{})),
		"jpeg": encodeJPEG(t, solid(1200, 900, red, color.RGBA{})),
	} {
		img, err := Decode(data, 256)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if b := img.RGBA.Bounds(); b != image.Rect(0, 0, 256, 256) || img.RGBA.Stride != 4*256 {
			t.Errorf("%s: bounds %v stride %d, want 256 square", name, b, img.RGBA.Stride)
		}
		if img.Bytes() != 256*256*4 {
			t.Errorf("%s: %d bytes", name, img.Bytes())
		}
		if !near(img.Average, red, 3) {
			t.Errorf("%s: average %v, want about %v", name, img.Average, red)
		}
	}
}

func TestDecodeWebPAndKeepsSmallImages(t *testing.T) {
	data, err := os.ReadFile("testdata/red.webp") // 64×48 lossless
	if err != nil {
		t.Fatal(err)
	}
	img, err := Decode(data, 256)
	if err != nil {
		t.Fatal(err)
	}
	// Cropped square, never scaled up.
	if b := img.RGBA.Bounds(); b != image.Rect(0, 0, 48, 48) {
		t.Errorf("bounds %v, want 48 square", b)
	}
	if want := (color.RGBA{200, 40, 40, 255}); !near(img.Average, want, 1) {
		t.Errorf("average %v, want %v", img.Average, want)
	}
}

func TestDecodeCropsTheMiddle(t *testing.T) {
	// Left half blue, right half green: the middle square keeps half of each.
	m := image.NewRGBA(image.Rect(0, 0, 200, 100))
	for y := range 100 {
		for x := range 200 {
			c := color.RGBA{0, 0, 255, 255}
			if x >= 100 {
				c = color.RGBA{0, 255, 0, 255}
			}
			m.SetRGBA(x, y, c)
		}
	}
	img, err := Decode(encodePNG(t, m), 100)
	if err != nil {
		t.Fatal(err)
	}
	if got := img.RGBA.RGBAAt(10, 50); got != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("left %v, want blue", got)
	}
	if got := img.RGBA.RGBAAt(90, 50); got != (color.RGBA{0, 255, 0, 255}) {
		t.Errorf("right %v, want green", got)
	}
}

func TestDecodeTrimsLetterbox(t *testing.T) {
	red := color.RGBA{200, 40, 40, 255}
	// hqdefault: 480×360, a 16:9 still between black bars.
	img, err := Decode(encodeJPEG(t, solid(480, 360, red, color.RGBA{2, 2, 2, 255})), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.RGBA.Bounds(); b != image.Rect(0, 0, 270, 270) {
		t.Errorf("bounds %v, want the 270 square of the still", b)
	}
	if !near(img.Average, red, 4) {
		t.Errorf("average %v: the bars show", img.Average)
	}
	// Shown small, it is scaled from the decoder's pixels.
	img, err = Decode(encodeJPEG(t, solid(480, 360, red, color.RGBA{2, 2, 2, 255})), 64)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.RGBA.Bounds(); b != image.Rect(0, 0, 64, 64) || !near(img.Average, red, 4) {
		t.Errorf("bounds %v, average %v: the bars show", b, img.Average)
	}
	// Bars that aren't black are part of the picture.
	img, err = Decode(encodePNG(t, solid(480, 360, red, color.RGBA{255, 255, 255, 255})), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.RGBA.Bounds(); b != image.Rect(0, 0, 360, 360) {
		t.Errorf("bounds %v, want 360 square", b)
	}
}

// atOnly hides RGBA64At, as an image of another decoder might.
type atOnly struct{ image.Image }

func TestTrimBarsWithoutRGBA64At(t *testing.T) {
	red := color.RGBA{200, 40, 40, 255}
	for _, band := range []color.RGBA{{2, 2, 2, 255}, {255, 255, 255, 255}} {
		m := solid(480, 360, red, band)
		if got, want := trimBars(rgba64(atOnly{m})), trimBars(m); got != want {
			t.Errorf("bars %v: trimmed to %v through At, %v through RGBA64At", band, got, want)
		}
	}
}

func TestAverageWeighsByAlpha(t *testing.T) {
	m := image.NewRGBA(image.Rect(0, 0, 2, 1))
	m.SetRGBA(0, 0, color.RGBA{100, 0, 0, 255})
	m.SetRGBA(1, 0, color.RGBA{}) // transparent: shows nothing
	if got := average(m); got != (color.RGBA{100, 0, 0, 255}) {
		t.Errorf("average %v", got)
	}
}

func TestPeekReaderDecodesAsBytesReader(t *testing.T) {
	for _, name := range []string{"jpeg/544", "png/544", "webp/544"} {
		data := benchFile()[name]
		want, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		got, _, err := image.Decode(&peekReader{data})
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: decoded otherwise through Peek: %v", name, err)
		}
	}
	if b, err := (&peekReader{[]byte("ab")}).Peek(3); string(b) != "ab" || !errors.Is(err, io.EOF) {
		t.Errorf("Peek past the end: %q, %v", b, err)
	}
}

func TestDecodeRejects(t *testing.T) {
	if _, err := Decode([]byte("<html>not found</html>"), 96); err == nil {
		t.Error("decoded HTML")
	}
	// A header alone: rejected before any pixel is read.
	ihdr := []byte("IHDR\x00\x00\x13\x88\x00\x00\x0f\xa0\x08\x00\x00\x00\x00") // 5000×4000, 8-bit gray
	hdr := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0d")
	hdr = append(hdr, ihdr...)
	hdr = binary.BigEndian.AppendUint32(hdr, crc32.ChecksumIEEE(ihdr))
	if _, err := Decode(hdr, 96); err == nil || !strings.Contains(err.Error(), "5000×4000") {
		t.Errorf("decoded a 20-megapixel image: %v", err)
	}
}

func TestShrinkSeesEveryPixel(t *testing.T) {
	checker := func(side int) *image.RGBA {
		m := image.NewRGBA(image.Rect(0, 0, side, side))
		for y := range side {
			for x := range side {
				c := color.RGBA{0, 0, 0, 255}
				if (x+y)%2 == 0 {
					c = color.RGBA{255, 255, 255, 255}
				}
				m.SetRGBA(x, y, c)
			}
		}
		return m
	}
	// A checkerboard of single pixels: sampling would show stripes or
	// moiré, averaging every pixel an even gray.
	for _, side := range []int{1200, 1001} {
		m := checker(side)
		// Through At too, as the decoders' images other than RGBA.
		for _, src := range []image.Image{m, atOnly{m}} {
			out := shrink(src, m.Rect, 256)
			if b := out.Bounds(); b != image.Rect(0, 0, 256, 256) {
				t.Fatalf("%d: bounds %v", side, b)
			}
			for y := range 256 {
				for x := range 256 {
					if c := out.RGBAAt(x, y); c.R < 110 || c.R > 145 || c.A != 255 {
						t.Fatalf("%d: pixel (%d, %d) is %v, want an even gray", side, x, y, c)
					}
				}
			}
		}
	}
	// Below twice the size none of the pattern is lost either.
	out := shrink(checker(300), image.Rect(0, 0, 300, 300), 256)
	if avg := average(out); avg.R < 120 || avg.R > 135 {
		t.Errorf("300: average %v, want gray", avg)
	}
	// Only the square asked for is read.
	m := solid(30, 30, color.RGBA{255, 0, 0, 255}, color.RGBA{})
	draw.Draw(m, image.Rect(10, 10, 20, 20), image.NewUniform(color.RGBA{0, 0, 255, 255}), image.Point{}, draw.Src)
	for _, src := range []image.Image{m, atOnly{m}} {
		if c := shrink(src, image.Rect(10, 10, 20, 20), 4).RGBAAt(3, 3); c != (color.RGBA{0, 0, 255, 255}) {
			t.Errorf("%T: read outside the square: %v", src, c)
		}
	}
}

// copyThenShrink decodes data as Decode did before its box filter: the
// crop copied whole, halved while twice the size, then bilinear. Its
// colors are Decode's: only the scaling differs.
func copyThenShrink(tb testing.TB, data []byte, px int) *image.RGBA {
	tb.Helper()
	src, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		tb.Fatal(err)
	}
	if format == "webp" {
		fullRange(src)
	}
	crop := square(trimBars(rgba64(src)))
	m := image.NewRGBA(image.Rect(0, 0, crop.Dx(), crop.Dx()))
	draw.Draw(m, m.Rect, src, crop.Min, draw.Src)
	for m.Rect.Dx() >= 2*px {
		m = halve(m)
	}
	if m.Rect.Dx() <= px {
		return m
	}
	dst := image.NewRGBA(image.Rect(0, 0, px, px))
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
		for x := range out.Rect.Dx() {
			left, right := 8*x, 4*min(2*x+1, w-1)
			for c := range 4 {
				sum := int(top[left+c]) + int(top[right+c]) + int(bottom[left+c]) + int(bottom[right+c])
				out.Pix[y*out.Stride+4*x+c] = uint8((sum + 2) / 4)
			}
		}
	}
	return out
}

// diff returns the mean and largest difference of a and b's channels.
func diff(a, b *image.RGBA) (mean float64, most int) {
	var sum int
	for i := range a.Pix {
		d := int(a.Pix[i]) - int(b.Pix[i])
		d = max(d, -d)
		sum += d
		most = max(most, d)
	}
	return float64(sum) / float64(len(a.Pix)), most
}

func TestDecodeLooksAsBefore(t *testing.T) {
	for _, tc := range []struct {
		name string
		px   int
	}{
		{"jpeg/544", 352},
		{"jpeg/544", 192},
		{"jpeg/hq720", 64},
		{"jpeg/hq720", 96},
		{"jpeg/hq720", 160},
		{"jpeg/hqdefault", 64},
		{"jpeg/1200", 96},
		{"jpeg/1200", 256},
		{"png/1200", 128},
		{"webp/1200", 288},
	} {
		data := benchFile()[tc.name]
		img, err := Decode(data, tc.px)
		if err != nil {
			t.Fatal(err)
		}
		want := copyThenShrink(t, data, tc.px)
		if img.RGBA.Rect != want.Rect {
			t.Fatalf("%s at %d: bounds %v, want %v", tc.name, tc.px, img.RGBA.Rect, want.Rect)
		}
		mean, most := diff(img.RGBA, want)
		t.Logf("%s at %d: mean %.2f, most %d", tc.name, tc.px, mean, most)
		if mean > 3 || most > 80 {
			t.Errorf("%s at %d: channels differ by %.2f on average, %d at most", tc.name, tc.px, mean, most)
		}
	}
}

// A lossy WebP decodes to the colors it was encoded from: x/image/webp's
// limited range, unexpanded, was ~8 levels off.
func TestDecodeWebPInFullRange(t *testing.T) {
	want, err := Decode(benchFile()["png/544"], coverPx)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(benchFile()["webp/544"], coverPx)
	if err != nil {
		t.Fatal(err)
	}
	var sum, bias int
	for i, v := range got.RGBA.Pix {
		d := int(v) - int(want.RGBA.Pix[i])
		sum += max(d, -d)
		bias += d
	}
	n := len(got.RGBA.Pix) * 3 / 4 // alpha is never off
	// 2.4 and -0.3 today: cwebp -q 80's loss; unexpanded, 7.4 and -2.9.
	if mean, off := float64(sum)/float64(n), float64(bias)/float64(n); mean > 4 || off > 1 || off < -1 {
		t.Errorf("mean difference %.2f levels, bias %.2f; want under 4 and 1", mean, off)
	}
}
