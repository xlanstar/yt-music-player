package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// ellipsis ends text cut to fit.
const ellipsis = "…"

// fit cuts s, which may hold styles, to w cells of display width (wide
// CJK, emoji), ending it with an ellipsis when cut.
func fit(s string, w int) string { return ansi.Truncate(s, w, ellipsis) }

// pad fits s to w cells and fills the rest with spaces.
func pad(s string, w int) string {
	s = fit(s, w)
	if n := w - width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// width is s's display width; s may hold styles.
func width(s string) int { return ansi.StringWidth(s) }

// spread puts left and right on one line of w cells after prefix, right
// flush to the end; left is cut first, then right.
func spread(prefix, left, right string, w int) string {
	rw := width(right)
	if rw >= w {
		return prefix + fit(right, w)
	}
	lw := width(left)
	if lw > w-rw-1 {
		left = fit(left, max(w-rw-1, 0))
		lw = width(left)
	}
	return prefix + left + strings.Repeat(" ", w-lw-rw) + right
}

// Clock says seconds as m:ss, or h:mm:ss from an hour; ytm status says
// times as the TUI does.
func Clock(seconds float64) string {
	s := max(int(seconds), 0)
	h, m := s/3600, s/60
	b := make([]byte, 0, len("100:00:00"))
	if h > 0 {
		m %= 60
		b = strconv.AppendInt(b, int64(h), 10)
		b = append(b, ':')
		if m < 10 {
			b = append(b, '0')
		}
	}
	b = strconv.AppendInt(b, int64(m), 10)
	b = append(b, ':')
	if s%60 < 10 {
		b = append(b, '0')
	}
	b = strconv.AppendInt(b, int64(s%60), 10)
	return string(b)
}
