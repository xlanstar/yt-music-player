package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// ellipsis ends text cut to fit.
const ellipsis = "…"

// fit cuts plain text s to w cells, by display width (wide CJK, emoji),
// ending it with an ellipsis when cut.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, ellipsis)
}

// pad fits s to w cells and fills the rest with spaces.
func pad(s string, w int) string {
	s = fit(s, w)
	if n := w - ansi.StringWidth(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// width is s's display width; s may hold styles.
func width(s string) int { return ansi.StringWidth(s) }

// spread puts left and right on one line of w cells, right flush to the
// end; left is cut first, then right.
func spread(left, right string, w int) string {
	rw := width(right)
	if rw >= w {
		return truncate(right, w)
	}
	lw := width(left)
	if lw > w-rw-1 {
		left = truncate(left, max(w-rw-1, 0))
		lw = width(left)
	}
	return left + strings.Repeat(" ", w-lw-rw) + right
}

// clock says seconds as m:ss, or h:mm:ss from an hour.
func clock(seconds float64) string {
	s := max(int(seconds), 0)
	h, m := s/3600, s/60
	var b []byte
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

// truncate cuts s, which may hold styles, to w cells, with an ellipsis.
func truncate(s string, w int) string { return ansi.Truncate(s, w, ellipsis) }
