package termimage

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/jpeg"
	"strings"
)

// iterm2Image returns img as an iTerm2 inline image over cols×rows cells, for
// Splice to draw. JPEG, as covers are photos: a few KB, and no zlib.
func iterm2Image(img *image.RGBA, cols, rows int, tmux bool) (string, error) {
	var data flushBuffer
	// Covers take about a byte for 8 pixels; small ones, their tables.
	data.Grow(img.Rect.Dx()*img.Rect.Dy()/8 + 1024)
	if err := jpeg.Encode(&data, img, &jpeg.Options{Quality: 85}); err != nil {
		return "", err
	}
	var b strings.Builder
	b.Grow(base64.StdEncoding.EncodedLen(data.Len()) + 128)
	b.WriteString("\x1b]1337;File=inline=1;size=")
	writeInt(&b, data.Len())
	b.WriteString(";width=")
	writeInt(&b, cols)
	b.WriteString(";height=")
	writeInt(&b, rows)
	b.WriteString(";preserveAspectRatio=0;doNotMoveCursor=1:")
	// In chunks of whole 3 bytes, as base64.NewEncoder, without its state.
	var enc [4096]byte
	for jpg := data.Bytes(); len(jpg) > 0; {
		n := min(len(jpg), len(enc)/4*3)
		base64.StdEncoding.Encode(enc[:], jpg[:n])
		b.Write(enc[:base64.StdEncoding.EncodedLen(n)])
		jpg = jpg[n:]
	}
	b.WriteByte('\a')
	if tmux {
		return passthrough(b.String()), nil
	}
	return b.String(), nil
}

// flushBuffer is a bytes.Buffer image/jpeg writes to as it is: with Flush,
// it needs no 4 KB bufio.Writer of its own.
type flushBuffer struct{ bytes.Buffer }

func (*flushBuffer) Flush() error { return nil }
