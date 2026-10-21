package tui

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"yt-music-player/internal/catalog"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// methods are the renderer's two ways to measure text, by name.
var methods = []struct {
	name   string
	method ansi.Method
}{{"wcwidth", ansi.WcWidth}, {"graphemes", ansi.GraphemeWidth}}

// unicodeCore is a terminal's answer that it knows mode 2027.
var unicodeCore = tea.ModeReportMsg{Mode: ansi.ModeUnicodeCore, Value: ansi.ModeReset}

// emojiSongs are titles the methods measure apart: a ZWJ sequence, a skin
// tone, an emoji presentation selector (VS16), a combining accent.
var emojiSongs = []catalog.Song{
	{ID: "zwj", Title: "👩‍💻 Night Shift 👨‍👩‍👧‍👦 Family", Artist: "Coder", Album: "Late", Seconds: 201},
	{ID: "tone", Title: "👍🏽 Thumbs Up 👍🏽", Artist: "米津玄師", Album: "STRAY SHEEP", Seconds: 372},
	{ID: "vs16", Title: "❤️ Heart ❤️ Heart ❤️", Artist: "Someone", Seconds: 99},
	{ID: "accent", Title: "Cafe\u0301 au lait", Artist: "Ame\u0301lie", Album: "Paris", Seconds: 180},
}

// texts are the widths a line can hold, each by method.
var texts = []struct {
	text   string
	wc, gr int
}{
	{"夜に駆ける", 10, 10},
	{"👩‍💻", 4, 2},
	{"👍🏽", 4, 2},
	{"❤️", 1, 2},
	{"e\u0301", 1, 1},
	{"\x1b[1m夜\x1b[m👩‍💻", 6, 4},
}

func TestCellsWidth(t *testing.T) {
	for _, m := range methods {
		c := newCells(m.method)
		for _, tc := range texts {
			want := tc.wc
			if m.method == ansi.GraphemeWidth {
				want = tc.gr
			}
			if got := c.width(tc.text); got != want {
				t.Errorf("%s: width(%q) = %d, want %d", m.name, tc.text, got, want)
			}
		}
		// The glyphs are measured, not assumed: each prints.
		for _, g := range []string{iconPlaying, iconPaused, iconWaiting, iconPlayAll, iconLiked, iconCurrent, iconWarn, ruleThin, ruleThick, ellipsis} {
			if c.width(g) < 1 {
				t.Errorf("%s: %q measures %d cells", m.name, g, c.width(g))
			}
		}
		want := cells{
			method: m.method,
			cursor: 1 + m.method.StringWidth(cursorMark),
			mark:   1 + max(m.method.StringWidth(iconCurrent), m.method.StringWidth(iconPlayAll), m.method.StringWidth(iconLiked)),
			heart:  1 + m.method.StringWidth(iconLiked),
			thin:   m.method.StringWidth(ruleThin),
			thick:  m.method.StringWidth(ruleThick),
		}
		if c != want {
			t.Errorf("%s: newCells = %+v, want %+v", m.name, c, want)
		}
	}
}

// Cut or wrapped, text takes the cells it is given, by
// either method.
func TestCellsFit(t *testing.T) {
	long := "夜に駆ける 👩‍💻👩‍💻 Night 👍🏽 shift ❤️❤️❤️ Cafe\u0301 \x1b[1mbold\x1b[m 群青"
	for _, m := range methods {
		c := newCells(m.method)
		for _, s := range []string{long, "👩‍💻👩‍💻", "❤️❤️❤️❤️", "夜夜夜", ""} {
			for w := range 24 {
				got := c.fit(s, w)
				switch gw := c.width(got); {
				case c.width(s) <= w && got != s:
					t.Errorf("%s: fit(%q, %d) = %q, cut what fits", m.name, s, w, got)
				case gw > w:
					t.Errorf("%s: fit(%q, %d) = %q, %d cells", m.name, s, w, got, gw)
				case got != s && w > 0 && !strings.HasSuffix(ansi.Strip(got), ellipsis):
					t.Errorf("%s: fit(%q, %d) = %q, without an ellipsis", m.name, s, w, got)
				}
			}
		}
		// The widest cluster is 4 cells wide by runes.
		for w := 4; w < 24; w++ {
			for line := range strings.Lines(c.wrap(long, w)) {
				if lw := c.width(strings.TrimSuffix(line, "\n")); lw > w {
					t.Errorf("%s: wrap(%d) made %q, %d cells", m.name, w, line, lw)
				}
			}
		}
	}
}

// Rules repeat their glyph by cells, spaces where a wide one no longer
// fits.
// cut keeps every style and the cells asked, a wide character split
// at either edge as spaces, by either method.
func TestCellsCut(t *testing.T) {
	sgr := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	lines := []string{"abcdef", "\x1b[1mab\x1b[m\x1b[90mcd\x1b[m"}
	for _, tc := range texts {
		lines = append(lines, tc.text, "a"+tc.text+"b")
	}
	for _, m := range methods {
		c := newCells(m.method)
		for _, line := range lines {
			w := c.width(line)
			for from := range w + 1 {
				for to := from; to <= w+1; to++ {
					got := c.cut(line, from, to)
					want := min(to, w) - from
					if c.width(got) != want || !slices.Equal(sgr.FindAllString(got, -1), sgr.FindAllString(line, -1)) {
						t.Errorf("%s: cells [%d, %d) of %q are %q, %d cells; want %d and every style",
							m.name, from, to, line, got, c.width(got), want)
					}
				}
			}
		}
	}
	if got := newCells(ansi.WcWidth).cut("猫の恩返し", 1, 7); got != " の恩 " {
		t.Errorf("cut %q, want the halves shown as spaces", got)
	}
}

func TestCellsRepeat(t *testing.T) {
	for _, m := range methods {
		c := newCells(m.method)
		for _, g := range []string{ruleThin, ruleThick, "夜", "👩‍💻"} {
			for n := -1; n < 9; n++ {
				var b strings.Builder
				c.writeRepeat(&b, g, n)
				if got := c.width(b.String()); got != max(n, 0) {
					t.Errorf("%s: writeRepeat(%q, %d) = %q, %d cells", m.name, g, n, b.String(), got)
				}
			}
		}
	}
}

// The renderer measures graphemes once the terminal knows mode 2027,
// whether set or not, and never goes back.
func TestCellsAfterReport(t *testing.T) {
	wc, gr := newCells(ansi.WcWidth), newCells(ansi.GraphemeWidth)
	for _, tc := range []struct {
		name   string
		from   cells
		report tea.ModeReportMsg
		want   cells
	}{
		{"reset", wc, unicodeCore, gr},
		{"set", wc, tea.ModeReportMsg{Mode: ansi.ModeUnicodeCore, Value: ansi.ModeSet}, gr},
		{"permanently set", wc, tea.ModeReportMsg{Mode: ansi.ModeUnicodeCore, Value: ansi.ModePermanentlySet}, gr},
		{"not recognized", wc, tea.ModeReportMsg{Mode: ansi.ModeUnicodeCore, Value: ansi.ModeNotRecognized}, wc},
		{"permanently reset", wc, tea.ModeReportMsg{Mode: ansi.ModeUnicodeCore, Value: ansi.ModePermanentlyReset}, wc},
		{"another mode", wc, tea.ModeReportMsg{Mode: ansi.ModeSynchronizedOutput, Value: ansi.ModeReset}, wc},
		{"again", gr, unicodeCore, gr},
		{"not back", gr, tea.ModeReportMsg{Mode: ansi.ModeUnicodeCore, Value: ansi.ModeNotRecognized}, gr},
	} {
		got, changed := tc.from.afterReport(tc.report)
		if got != tc.want || changed != (tc.want != tc.from) {
			t.Errorf("%s: afterReport = %+v, %t; want %+v", tc.name, got, changed, tc.want)
		}
	}
}

// TestWidthMethods lays the queue out by each method, as the renderer
// measures it: no line past the screen, the selected row reversed to its
// end, the songs' lengths in one column.
func TestWidthMethods(t *testing.T) {
	for _, m := range methods {
		t.Run(m.name, func(t *testing.T) {
			h := placeholderHarness(t)
			if m.method == ansi.GraphemeWidth {
				h.send(unicodeCore)
			}
			songs := append(slices.Clone(emojiSongs), wideSongs...)
			h.mirror.setQueue(songs)
			h.mirror.state.Playing = true
			h.send(postedMsg{})
			h.press("down", "down")
			for _, w := range []int{80, 63, 44, 30} {
				h.resize(w, 16)
				c := h.m.cells
				for line := range strings.Lines(h.m.content) {
					line = strings.TrimSuffix(line, "\n")
					lw := c.width(line)
					if lw > w || strings.Contains(line, "▸") && lw != w {
						t.Errorf("%d wide: a line of %d cells: %q", w, lw, line)
					}
				}
				if cols := lengthColumns(t, c, h.screen(), len(songs)); slices.ContainsFunc(cols, func(x int) bool { return x != cols[0] }) {
					t.Errorf("%d wide: songs' lengths end at cells %v:\n%s", w, cols, h.screen())
				}
			}
		})
	}
}

// lengthColumns returns the cell of the last colon of the first n rows
// under the tabs: where each song's length shows.
func lengthColumns(t *testing.T, c cells, screen string, n int) []int {
	t.Helper()
	lines := strings.Split(screen, "\n")[tabsRows : tabsRows+n]
	cols := make([]int, len(lines))
	for i, line := range lines {
		before, _, ok := strings.CutLast(line, ":")
		if !ok {
			t.Fatalf("row %d shows no length: %q", i, line)
		}
		cols[i] = c.width(before)
	}
	return cols
}

// The terminal's answer renders every line again by the new method;
// nothing kept between renders holds a width but the cells.
func TestWidthMethodSwitch(t *testing.T) {
	h := placeholderHarness(t)
	h.mirror.setQueue(emojiSongs)
	h.send(postedMsg{})
	byRunes, frame := h.m.content, h.m.covers.frame
	h.send(unicodeCore)
	if h.m.covers.frame == frame {
		t.Fatal("the answer rendered nothing")
	}
	if h.m.cells != newCells(ansi.GraphemeWidth) {
		t.Errorf("cells %+v after the answer", h.m.cells)
	}
	if h.m.content == byRunes {
		t.Error("the screen kept the lines laid out by runes")
	}
	fresh := placeholderHarness(t)
	fresh.m.cells = newCells(ansi.GraphemeWidth)
	fresh.mirror.setQueue(emojiSongs)
	fresh.send(postedMsg{})
	if h.m.content != fresh.m.content {
		t.Errorf("after the answer:\n%s\nwant, as by graphemes from the start:\n%s", h.screen(), fresh.screen())
	}
	frame = h.m.covers.frame
	if h.send(unicodeCore); h.m.covers.frame != frame {
		t.Error("the same answer again rendered the screen again")
	}
}
