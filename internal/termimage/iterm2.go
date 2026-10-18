package termimage

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/jpeg"
	"strconv"
	"strings"
)

// ITerm2Image returns img as an iTerm2 inline image over cols×rows cells, for
// Overlay to draw. JPEG, as covers are photos: a few KB, and no zlib.
func ITerm2Image(img *image.RGBA, cols, rows int, tmux bool) (string, error) {
	var data bytes.Buffer
	data.Grow(img.Rect.Dx() * img.Rect.Dy() / 4)
	if err := jpeg.Encode(&data, img, &jpeg.Options{Quality: 85}); err != nil {
		return "", err
	}
	var b strings.Builder
	b.Grow(base64.StdEncoding.EncodedLen(data.Len()) + 128)
	b.WriteString("\x1b]1337;File=inline=1;size=")
	b.WriteString(strconv.Itoa(data.Len()))
	b.WriteString(";width=")
	b.WriteString(strconv.Itoa(cols))
	b.WriteString(";height=")
	b.WriteString(strconv.Itoa(rows))
	b.WriteString(";preserveAspectRatio=0;doNotMoveCursor=1:")
	enc := base64.NewEncoder(base64.StdEncoding, &b)
	_, _ = enc.Write(data.Bytes())
	_ = enc.Close()
	b.WriteByte('\a')
	if tmux {
		return Passthrough(b.String()), nil
	}
	return b.String(), nil
}
