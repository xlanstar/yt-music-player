package termimage

import "image"

// Fill returns src cropped to w×h's shape around its middle and scaled to
// w×h, each pixel the mean of those it covers: a box filter, which a cover
// shrunk a few times needs and costs one pass.
func Fill(src *image.RGBA, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	b := src.Rect
	sw, sh := b.Dx(), b.Dy()
	if sw <= 0 || sh <= 0 || w <= 0 || h <= 0 {
		return dst
	}
	// The largest w:h rectangle of src, centered.
	cw, ch := sw, sw*h/w
	if ch > sh {
		cw, ch = sh*w/h, sh
	}
	cw, ch = max(cw, 1), max(ch, 1)
	cx, cy := (sw-cw)/2, (sh-ch)/2
	for y := range h {
		y0 := cy + y*ch/h
		y1 := max(cy+(y+1)*ch/h, y0+1)
		row := dst.Pix[y*dst.Stride:]
		for x := range w {
			x0 := cx + x*cw/w
			x1 := max(cx+(x+1)*cw/w, x0+1)
			var r, g, bl, a, n uint32
			for sy := y0; sy < y1; sy++ {
				p := src.Pix[sy*src.Stride+4*x0 : sy*src.Stride+4*x1]
				for i := 0; i+3 < len(p); i += 4 {
					r += uint32(p[i])
					g += uint32(p[i+1])
					bl += uint32(p[i+2])
					a += uint32(p[i+3])
					n++
				}
			}
			o := row[4*x : 4*x+4 : 4*x+4]
			o[0], o[1], o[2], o[3] = mean(r, n), mean(g, n), mean(bl, n), mean(a, n)
		}
	}
	return dst
}

func mean(sum, n uint32) uint8 { return uint8((sum + n/2) / n) } //nolint:gosec // the mean of bytes
