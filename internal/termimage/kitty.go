package termimage

import (
	"encoding/base64"
	"image"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi/kitty"
)

// kittyChunk is the most base64 a Kitty command carries.
const kittyChunk = 4096

// KittyUpload returns the commands that send img as image id, placed
// virtually over cols×rows cells for KittyCells to show. Raw RGB, not zlib:
// a compressor's state outweighs a cover, which is sent once.
func KittyUpload(img *image.RGBA, id uint8, cols, rows int, tmux bool) string {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	n := base64.StdEncoding.EncodedLen(3 * w * h)
	chunks := max((n+kittyChunk-1)/kittyChunk, 1)
	head := "\x1b_Ga=T,U=1,q=2,f=24,s=" + strconv.Itoa(w) + ",v=" + strconv.Itoa(h) +
		",i=" + strconv.Itoa(int(id)) + ",c=" + strconv.Itoa(cols) + ",r=" + strconv.Itoa(rows)
	var b strings.Builder
	size := n + len(head) + chunks*len("\x1b_Gm=1;\x1b\\")
	if tmux {
		size += chunks * len("\x1bPtmux;\x1b\x1b\\\x1b\\")
	}
	b.Grow(size)
	// 3072 pixels' bytes base64 into one chunk; the image is never held
	// as RGB or base64 whole.
	var raw [kittyChunk / 4 * 3]byte
	var enc [kittyChunk]byte
	i := 0
	for c := range chunks {
		k := 0
		for ; k+3 <= len(raw) && i < w*h; i++ {
			px := raw[k : k+3 : k+3]
			px[0], px[1], px[2] = rgb(img, i%w, i/w)
			k += 3
		}
		esc := "\x1b"
		if tmux {
			b.WriteString("\x1bPtmux;")
			esc = "\x1b\x1b"
		}
		b.WriteString(esc)
		if c == 0 {
			b.WriteString(head[1:])
			b.WriteByte(',')
		} else {
			b.WriteString("_G")
		}
		if c == chunks-1 {
			b.WriteString("m=0;")
		} else {
			b.WriteString("m=1;")
		}
		base64.StdEncoding.Encode(enc[:], raw[:k])
		b.Write(enc[:base64.StdEncoding.EncodedLen(k)])
		b.WriteString(esc)
		b.WriteByte('\\')
		if tmux {
			b.WriteString("\x1b\\")
		}
	}
	return b.String()
}

// KittyCells returns the lines of Unicode placeholders that show image id
// over cols×rows cells; its id is their 256-color foreground, which a
// renderer keeps where it would lower a true color.
func KittyCells(id uint8, cols, rows int) []string {
	fg := "\x1b[38;5;" + strconv.Itoa(int(id)) + "m"
	lines := make([]string, rows)
	var b strings.Builder
	for y := range rows {
		b.Grow(len(fg) + cols*8 + len("\x1b[39m"))
		b.WriteString(fg)
		for x := range cols {
			b.WriteRune(kitty.Placeholder)
			b.WriteRune(kitty.Diacritic(y))
			b.WriteRune(kitty.Diacritic(x))
		}
		b.WriteString("\x1b[39m")
		lines[y] = b.String()
		b.Reset()
	}
	return lines
}

// KittyDelete returns the command that frees image id's pixels.
func KittyDelete(id uint8, tmux bool) string {
	seq := "\x1b_Ga=d,d=I,q=2,i=" + strconv.Itoa(int(id)) + "\x1b\\"
	if tmux {
		return Passthrough(seq)
	}
	return seq
}
