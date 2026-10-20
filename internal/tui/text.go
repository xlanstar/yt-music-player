package tui

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// cells measures, cuts and wraps text, which may hold styles, in a
// terminal's cells as Bubble Tea's renderer does: by rune (wcwidth), by
// grapheme once the terminal knows mode 2027 (afterReport). The model
// keeps one, which every width of a frame goes through.
type cells struct {
	method ansi.Method
	// The columns rows give glyphs, measured once per method: the
	// selected row's cursor, a row's mark (♪, ▶ or ♥, and a space) and a
	// song's heart (a space and ♥).
	cursor, mark, heart int
	// thin and thick are ruleThin's and ruleThick's widths.
	thin, thick int
}

func newCells(method ansi.Method) cells {
	c := cells{method: method}
	c.cursor = c.width(cursorMark)
	c.mark = max(c.width(iconCurrent), c.width(iconPlayAll), c.width(iconLiked)) + 1
	c.heart = 1 + c.width(iconLiked)
	c.thin, c.thick = c.width(ruleThin), c.width(ruleThick)
	return c
}

// afterReport returns the cells the renderer measures by once the
// terminal answered r, and whether they changed: Bubble Tea v2.1.0
// measures graphemes once the terminal knows mode 2027, set or not, and
// never goes back.
func (c cells) afterReport(r tea.ModeReportMsg) (cells, bool) {
	if r.Mode != ansi.ModeUnicodeCore || c.method == ansi.GraphemeWidth {
		return c, false
	}
	switch r.Value {
	case ansi.ModeReset, ansi.ModeSet, ansi.ModePermanentlySet:
		return newCells(ansi.GraphemeWidth), true
	}
	return c, false
}

// width is s's width in cells.
func (c cells) width(s string) int { return c.method.StringWidth(s) }

// fit cuts s to w cells, ending it with an ellipsis when cut; a wide
// character is never split.
func (c cells) fit(s string, w int) string {
	if c.width(s) <= w {
		return s
	}
	if c.method != ansi.GraphemeWidth && ansi.StringWidth(s) <= w {
		// Truncate keeps s whole where its graphemes fit, whatever its
		// method (x/ansi v0.11.9); spaces past the cut make them not.
		s += strings.Repeat(" ", w+1)
	}
	return c.method.Truncate(s, w, ellipsis)
}

// cut returns cells [from, to) of s, a line that may hold styles: all
// its styles, and spaces for the cells shown of a wide character the cut
// splits.
func (c cells) cut(s string, from, to int) string {
	var b strings.Builder
	b.Grow(len(s))
	at, state := 0, byte(0)
	for len(s) > 0 {
		seq, w, n, next := c.method.DecodeSequenceInString(s, state, nil)
		s, state = s[n:], next
		lo, hi := max(at, from), min(at+w, to)
		switch {
		case w == 0 || lo == at && hi == at+w:
			// Styles and zero widths go whatever their cells.
			b.WriteString(seq)
		case hi > lo:
			writeSpaces(&b, hi-lo)
		}
		at += w
	}
	return b.String()
}

// pad fits s to w cells and fills the rest with spaces.
func (c cells) pad(s string, w int) string {
	s = c.fit(s, w)
	if n := w - c.width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// wrap breaks s into lines of at most w cells, between words where it
// can.
func (c cells) wrap(s string, w int) string { return c.method.Wrap(s, w, "") }

// spread puts left and right on one line of w cells after prefix, right
// flush to the end; left is cut first, then right.
func (c cells) spread(prefix, left, right string, w int) string {
	rw := c.width(right)
	if rw >= w {
		return prefix + c.fit(right, w)
	}
	lw := c.width(left)
	if lw > w-rw-1 {
		left = c.fit(left, max(w-rw-1, 0))
		lw = c.width(left)
	}
	return prefix + left + strings.Repeat(" ", w-lw-rw) + right
}

// writeRepeat fills n cells of b with glyph, as many times as it fits,
// and with spaces in the cells a wide glyph leaves.
func (c cells) writeRepeat(b *strings.Builder, glyph string, n int) {
	if gw := c.glyphWidth(glyph); gw > 0 {
		for range n / gw {
			b.WriteString(glyph)
		}
		n %= gw
	}
	writeSpaces(b, n)
}

// glyphWidth is glyph's width; the rules' are measured once per method.
func (c cells) glyphWidth(glyph string) int {
	switch glyph {
	case ruleThin:
		return c.thin
	case ruleThick:
		return c.thick
	}
	return c.width(glyph)
}

// writeRule writes n cells of glyph to b in p's style.
func (c cells) writeRule(b *strings.Builder, p paint, glyph string, n int) {
	if n <= 0 {
		return
	}
	b.WriteString(p.pre)
	c.writeRepeat(b, glyph, n)
	b.WriteString(p.post)
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
