package tui

import (
	"image"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// newScene is an empty w×h scene measured by rune, and its screen.
func newScene(w, h int) (*scene, region) {
	s := &scene{}
	return s, s.reset(w, h, newCells(ansi.WcWidth))
}

// composed is s composed without covers' lines.
func composed(s *scene) string { return s.compose(&coverFrame{}) }

// wantPanic fails t unless draw panics.
func wantPanic(t *testing.T, what string, draw func()) {
	t.Helper()
	defer func() {
		t.Helper()
		if recover() == nil {
			t.Errorf("%s drew over cells drawn", what)
		}
	}()
	draw()
}

// Regions lay out where their parents say, clipped by them, and clamp
// cuts and insets past their size.
func TestRegionLayout(t *testing.T) {
	_, screen := newScene(10, 6)
	inner := screen.inset(1, 1, 1, 1)
	above := screen.sub(image.Rect(0, -2, 10, 2))
	_, aboveRest := above.cutTop(1)
	right := screen.sub(image.Rect(5, 0, 15, 6))
	rect := func(x0, y0, x1, y1 int) image.Rectangle {
		return image.Rectangle{Min: image.Pt(x0, y0), Max: image.Pt(x1, y1)}
	}
	first := func(a, _ region) region { return a }
	second := func(_, b region) region { return b }
	for _, tc := range []struct {
		name         string
		g            region
		bounds, clip image.Rectangle
	}{
		{"screen", screen, rect(0, 0, 10, 6), rect(0, 0, 10, 6)},
		{"inset", inner, rect(1, 1, 9, 5), rect(1, 1, 9, 5)},
		{"inset of inset", inner.inset(2, 0, 0, 1), rect(3, 1, 9, 4), rect(3, 1, 9, 4)},
		{"negative insets", inner.inset(-2, -2, -2, -2), rect(1, 1, 9, 5), rect(1, 1, 9, 5)},
		{"insets past the width", screen.inset(6, 0, 6, 0), rect(6, 0, 6, 6), image.Rectangle{}},
		{"insets past the height", screen.inset(0, 9, 0, 9), rect(0, 6, 10, 6), image.Rectangle{}},
		{"top", first(screen.cutTop(2)), rect(0, 0, 10, 2), rect(0, 0, 10, 2)},
		{"below the top", second(screen.cutTop(2)), rect(0, 2, 10, 6), rect(0, 2, 10, 6)},
		{"no top", first(screen.cutTop(-1)), rect(0, 0, 10, 0), image.Rectangle{}},
		{"all top", first(screen.cutTop(9)), rect(0, 0, 10, 6), rect(0, 0, 10, 6)},
		{"nothing below all", second(screen.cutTop(9)), rect(0, 6, 10, 6), image.Rectangle{}},
		{"left of the inset", first(inner.cutLeft(4)), rect(1, 1, 5, 5), rect(1, 1, 5, 5)},
		{"right of the inset", second(inner.cutLeft(4)), rect(5, 1, 9, 5), rect(5, 1, 9, 5)},
		{"all left", second(inner.cutLeft(20)), rect(9, 1, 9, 5), image.Rectangle{}},
		// The bar too tall for the screen keeps its last rows: its rows
		// keep their places, those above the screen hidden.
		{"above the screen", above, rect(0, -2, 10, 2), rect(0, 0, 10, 2)},
		{"cut above the screen", first(above.cutTop(1)), rect(0, -2, 10, -1), image.Rectangle{}},
		{"its rest", aboveRest, rect(0, -1, 10, 2), rect(0, 0, 10, 2)},
		{"its rest's second row", first(second(aboveRest.cutTop(1)).cutTop(1)), rect(0, 0, 10, 1), rect(0, 0, 10, 1)},
		{"past the right edge", right, rect(5, 0, 15, 6), rect(5, 0, 10, 6)},
		{"inset past the right edge", right.inset(2, 0, 0, 0), rect(7, 0, 15, 6), rect(7, 0, 10, 6)},
		{"wholly past it", right.inset(6, 0, 0, 0), rect(11, 0, 15, 6), image.Rectangle{}},
		{"negative size", screen.sub(rect(3, 3, 1, 1)), rect(3, 3, 3, 3), image.Rectangle{}},
		{"empty screen", (&scene{}).reset(0, 0, cells{}), rect(0, 0, 0, 0), image.Rectangle{}},
	} {
		if tc.g.bounds != tc.bounds || !tc.g.clip.Empty() && tc.g.clip != tc.clip || tc.g.clip.Empty() != tc.clip.Empty() {
			t.Errorf("%s: bounds %v, clip %v; want %v, %v", tc.name, tc.g.bounds, tc.g.clip, tc.bounds, tc.clip)
		}
		if w, h := tc.g.size(); w != tc.bounds.Dx() || h != tc.bounds.Dy() {
			t.Errorf("%s: size %d×%d, want its bounds'", tc.name, w, h)
		}
	}
}

// Text goes where its region is, spaces between, whatever the order it
// is drawn in; what its region does not show is cut, by cells.
func TestRegionText(t *testing.T) {
	bold := paint{pre: "\x1b[1m", post: "\x1b[m"}
	for _, tc := range []struct {
		name string
		draw func(screen region)
		want string
	}{
		{"right column first", func(screen region) {
			left, right := screen.cutLeft(5)
			right.text(0, 0, "right", paint{})
			left.text(0, 1, "under", paint{})
			left.text(0, 0, "left", paint{})
		}, "left right\nunder"},
		{"inset", func(screen region) {
			screen.inset(2, 1, 0, 0).text(1, 0, "x", paint{})
		}, "\n   x"},
		{"cut right", func(screen region) { screen.text(7, 0, "abcdef", paint{}) }, "       abc"},
		{"cut left", func(screen region) {
			screen.sub(image.Rect(-3, 0, 7, 1)).text(0, 0, "abcdef", paint{})
		}, "def"},
		{"cut by the parent", func(screen region) {
			screen.inset(0, 0, 6, 0).sub(image.Rect(2, 0, 8, 1)).text(0, 0, "abcdef", paint{})
		}, "  ab"},
		{"rows past the screen", func(screen region) {
			screen.text(0, -1, "above", paint{})
			screen.text(0, 3, "below", paint{})
			screen.text(0, 2, "last", paint{})
		}, "\n\nlast"},
		{"painted, cut", func(screen region) { screen.text(6, 0, "abcdef", bold) }, "      \x1b[1mabcd\x1b[m"},
		{"styled, cut", func(screen region) { screen.text(8, 0, "\x1b[1mab\x1b[mcd", paint{}) }, "        \x1b[1mab\x1b[m"},
		{"wide, cut right", func(screen region) { screen.text(7, 0, "猫の", paint{}) }, "       猫 "},
		{"wide, cut left", func(screen region) {
			screen.sub(image.Rect(-1, 0, 9, 1)).text(0, 0, "猫の", paint{})
		}, " の"},
		{"empty text", func(screen region) { screen.text(0, 0, "", bold) }, ""},
		{"empty region", func(screen region) { screen.inset(5, 0, 5, 0).text(0, 0, "x", paint{}) }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, screen := newScene(10, 3)
			tc.draw(screen)
			if got := composed(s); got != tc.want {
				t.Errorf("composed %q, want %q", got, tc.want)
			}
			for y, row := range s.rows {
				for _, sp := range row {
					if got := s.cells.width(sp.paint.pre + sp.text + sp.paint.post); got != sp.cells || sp.x < 0 || sp.x+sp.cells > 10 {
						t.Errorf("row %d: %q from %d holds %d cells, measures %d", y, sp.text, sp.x, sp.cells, got)
					}
				}
			}
		})
	}
}

// By either method, text cut at either edge fills the cells shown and
// no more: a wide character or emoji split shows as spaces.
func TestRegionTextMethods(t *testing.T) {
	for _, m := range methods {
		for _, tc := range texts {
			for x := -6; x <= 10; x++ {
				s := &scene{}
				screen := s.reset(10, 1, newCells(m.method))
				line := "a" + tc.text + "b"
				w := screen.sub(image.Rect(2, 0, 8, 1)).text(x, 0, line, paint{})
				shown := min(x+w, 6) - max(x, 0)
				got := s.cells.width(composed(s))
				if shown > 0 && got != 2+max(x, 0)+shown || shown <= 0 && got != 0 {
					t.Errorf("%s: %q from cell %d of 6 shows %q, %d cells", m.name, line, x, composed(s), got)
				}
			}
		}
	}
}

// text returns its text's width, shown or not, to lay out what follows.
func TestRegionTextWidth(t *testing.T) {
	_, screen := newScene(10, 1)
	for _, tc := range []struct {
		g    region
		text string
		want int
	}{
		{screen, "猫の", 4},
		{screen.inset(9, 0, 0, 0), "猫の", 4},
		{screen.inset(10, 0, 0, 0), "abc", 3},
	} {
		if got := tc.g.text(0, 0, tc.text, paint{}); got != tc.want {
			t.Errorf("%q in %v: %d cells, want %d", tc.text, tc.g.clip, got, tc.want)
		}
	}
}

// fit draws text fitted as cells.fit fits it, and returns its width.
func TestRegionFit(t *testing.T) {
	bold := paint{pre: "\x1b[1m", post: "\x1b[m"}
	for _, tc := range []struct {
		text string
		n    int
		want string
	}{
		{"abc", 3, " \x1b[1mabc\x1b[m"},
		{"abcd", 3, " \x1b[1mab…\x1b[m"},
		{"猫の", 3, " \x1b[1m猫…\x1b[m"},
		{"abc", 0, ""},
		{"abc", -1, ""},
	} {
		s, screen := newScene(10, 1)
		w := screen.fit(1, 0, tc.text, tc.n, bold)
		if got := composed(s); got != tc.want || w != s.cells.width(ansi.Strip(got))-1 && got != "" {
			t.Errorf("%q in %d cells: %q, %d cells; want %q", tc.text, tc.n, got, w, tc.want)
		}
	}
}

// A run cut takes what fit leaves of the line its pieces make, and puts
// the ellipsis in the paint of the piece cut, by either method.
func TestRegionRun(t *testing.T) {
	bold := paint{pre: "\x1b[1m", post: "\x1b[m"}
	muted := paint{pre: "\x1b[90m", post: "\x1b[m"}
	for _, m := range methods {
		c := newCells(m.method)
		for _, ps := range [][]piece{
			{{text: "▶", paint: bold}, {text: "Midnight", gap: 1}, {text: " · Lumen", paint: muted}},
			{{text: "夜に", paint: bold, gap: 1}, {text: "👩‍💻a", gap: 2}, {gap: 1}},
			{{text: "\x1b[1;36menter\x1b[m Retry"}, {text: "e\u0301❤️", paint: muted, gap: 1}},
			{{gap: 2}, {text: "x", paint: bold}},
		} {
			var line strings.Builder
			for _, p := range ps {
				line.WriteString(strings.Repeat(" ", p.gap))
				line.WriteString(p.paint.Render(p.text))
			}
			for n := -1; n <= c.width(line.String())+1; n++ {
				s := &scene{}
				w := s.reset(30, 1, c).run(2, 0, n, slices.Clone(ps))
				got := strings.TrimPrefix(composed(s), "  ")
				fit := c.fit(line.String(), n)
				want := strings.TrimRight(ansi.Strip(fit), " ")
				if ansi.Strip(got) != want || w != c.width(fit) {
					t.Errorf("%s: %q in %d cells: %q, %d cells; want %q", m.name, line.String(), n, got, w, want)
				}
			}
		}
	}
	for _, tc := range []struct {
		n    int
		want string
	}{
		{9, "\x1b[1mab\x1b[m \x1b[90mcdefgh\x1b[m"},
		{6, "\x1b[1mab\x1b[m \x1b[90mcd…\x1b[m"},
		{4, "\x1b[1mab\x1b[m \x1b[90m…\x1b[m"},
		{3, "\x1b[1mab\x1b[m…"},
		{2, "\x1b[1ma…\x1b[m"},
	} {
		s, screen := newScene(10, 1)
		screen.run(0, 0, tc.n, []piece{{text: "ab", paint: bold}, {text: "cdefgh", paint: muted, gap: 1}})
		if got := composed(s); got != tc.want {
			t.Errorf("cut to %d: %q, want %q", tc.n, got, tc.want)
		}
	}
}

// line draws a line fitted to the rest of its row, which it holds; cut
// on the left, it measures and cuts as text does.
func TestRegionLine(t *testing.T) {
	s, screen := newScene(10, 3)
	lines := screen.sub(image.Rect(0, -1, 10, 2))
	lines.line(0, 0, "cut")
	lines.line(0, 1, "one")
	lines.line(2, 2, "two")
	lines.line(0, 3, "below")
	lines.line(12, 1, "past")
	screen.sub(image.Rect(-2, 2, 8, 3)).line(0, 0, "abcd")
	if got, want := composed(s), "one\n  two\ncd"; got != want {
		t.Errorf("composed %q, want %q", got, want)
	}
	if sp := s.rows[1][0]; sp.x != 2 || sp.cells != 8 {
		t.Errorf("the line holds %d cells from %d, want the row's 8 from 2", sp.cells, sp.x)
	}
	if sp := s.rows[2][0]; sp.cells != 2 {
		t.Errorf("the line cut holds %d cells, want 2", sp.cells)
	}
	wantPanic(t, "text after a line", func() { screen.text(8, 1, "x", paint{}) })
}

// Two columns side by side, each its own padding, draw in any order.
func TestSceneColumns(t *testing.T) {
	s, screen := newScene(24, 3)
	left, right := screen.cutLeft(10)
	right = right.inset(1, 0, 0, 0)
	left = left.inset(1, 0, 1, 0)
	for y, line := range []string{"Queue", "Search"} {
		right.text(0, y, line, paint{})
	}
	for y, line := range []string{"Midnight Arcade", "Neon"} {
		left.text(0, y, line, paint{})
	}
	if want := " Midnight  Queue\n Neon      Search"; composed(s) != want {
		t.Errorf("composed\n%s\nwant\n%s", composed(s), want)
	}
}

// Text, hits and covers drawn at the same cells of a region land on the
// same cells of the screen; a hit keeps the cells it spans as laid out,
// and clicks hit those shown.
func TestRegionTransforms(t *testing.T) {
	s, screen := newScene(10, 6)
	// A region two cells right and one above the screen, cut by its right
	// edge.
	g := screen.sub(image.Rect(2, -1, 14, 4))
	g.text(5, 2, "text", paint{})
	g.hit(image.Rect(5, 2, 9, 3), hit{kind: hitRow})
	g.hit(image.Rect(0, 0, 12, 1), hit{kind: hitProgress})
	g.hit(image.Rect(6, 3, 12, 4), hit{kind: hitProgress})
	g.cover(image.Rect(0, 2, 4, 4), slotBar, coverURL)

	sp := s.rows[1][1]
	if sp.x != 7 || sp.cells != 3 || sp.text != "tex" {
		t.Errorf("the text is %q from %d, %d cells; want %q from 7", sp.text, sp.x, sp.cells, "tex")
	}
	if len(s.hits) != 2 {
		t.Fatalf("hits %+v, want 2: the one above the screen is not", s.hits)
	}
	if h := s.hits[0]; h.rect != image.Rect(7, 1, 11, 2) || h.shown != image.Rect(7, 1, 10, 2) {
		t.Errorf("the text's hit spans %v, shows %v", h.rect, h.shown)
	}
	// The progress keeps its cells past the edge, for the drag's ratio.
	if h := s.hits[1]; h.rect != image.Rect(8, 2, 14, 3) || h.shown != image.Rect(8, 2, 10, 3) {
		t.Errorf("the progress spans %v, shows %v", h.rect, h.shown)
	}
	if len(s.covers) != 1 || s.covers[0].rect != image.Rect(2, 1, 6, 3) {
		t.Errorf("covers %+v, want one on (2, 1)–(6, 3)", s.covers)
	}
	m := &model{}
	m.scene.hits = s.hits
	for _, tc := range []struct {
		x, y int
		want hitKind
	}{{7, 1, hitRow}, {9, 1, hitRow}, {10, 1, 0}, {6, 1, 0}, {9, 2, hitProgress}, {7, 2, 0}} {
		if h, _ := m.hitAt(tc.x, tc.y); h.kind != tc.want {
			t.Errorf("(%d, %d) hits %d, want %d", tc.x, tc.y, h.kind, tc.want)
		}
	}
}

// coverLines is a cover frame of the bar's 4×2 cover: an overlay, marked.
var coverLines = coverFrame{
	lines: [numSlots][]string{slotBar: {"AAAA", "BBBB"}},
	marks: [numSlots][]string{slotBar: {"<0>", "<1>"}},
}

// A cover holds its cells, whose lines and marks covers readied; one not
// wholly shown is none, its cells blank.
func TestRegionCover(t *testing.T) {
	for _, tc := range []struct {
		name string
		draw func(screen region)
		// want is composed, covers is the covers drawn.
		want   string
		covers int
	}{
		{"shown", func(screen region) {
			_, rest := screen.cutTop(1)
			rest.cover(image.Rect(1, 0, 5, 2), slotBar, coverURL)
			rest.text(6, 0, "song", paint{})
			rest.text(6, 1, "0:41", paint{})
		}, "\n <0>AAAA song\n <1>BBBB 0:41", 1},
		{"drawn after its text", func(screen region) {
			screen.text(6, 0, "song", paint{})
			screen.cover(image.Rect(1, 0, 5, 2), slotBar, coverURL)
		}, " <0>AAAA song\n <1>BBBB", 1},
		{"top cut", func(screen region) {
			bar := screen.sub(image.Rect(0, -1, 10, 1))
			bar.cover(image.Rect(1, 0, 5, 2), slotBar, coverURL)
			bar.text(6, 1, "0:41", paint{})
		}, "      0:41", 0},
		{"cut by its parent", func(screen region) {
			screen.inset(0, 0, 7, 0).cover(image.Rect(1, 0, 5, 2), slotBar, coverURL)
		}, "", 0},
		{"below the screen", func(screen region) {
			screen.cover(image.Rect(1, 2, 5, 4), slotBar, coverURL)
		}, "", 0},
		{"no art", func(screen region) { screen.cover(image.Rect(1, 0, 5, 2), slotBar, "") }, "", 0},
		{"no lines yet", func(screen region) {
			screen.cover(image.Rect(1, 0, 3, 1), slotHeader, coverURL)
			screen.text(4, 0, "x", paint{})
		}, "    x", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, screen := newScene(10, 3)
			tc.draw(screen)
			if got := s.compose(&coverLines); got != tc.want || len(s.covers) != tc.covers {
				t.Errorf("composed %q, %d covers; want %q, %d", got, len(s.covers), tc.want, tc.covers)
			}
		})
	}
}

// Drawing over cells drawn is a layout's mistake: tests panic, a release
// keeps what came first.
func TestSceneOverlap(t *testing.T) {
	cases := []struct {
		name string
		draw func(screen region)
	}{
		{"text over text", func(screen region) {
			screen.text(2, 0, "first", paint{})
			screen.text(0, 0, "abc", paint{})
		}},
		{"text over a cover", func(screen region) {
			screen.cover(image.Rect(1, 0, 5, 2), slotBar, coverURL)
			screen.text(0, 1, "secret", paint{})
		}},
		{"a cover over text", func(screen region) {
			screen.text(4, 1, "first", paint{})
			screen.cover(image.Rect(1, 0, 5, 2), slotBar, coverURL)
		}},
		{"a slot twice", func(screen region) {
			screen.cover(image.Rect(1, 0, 5, 2), slotBar, coverURL)
			screen.cover(image.Rect(6, 0, 10, 2), slotBar, coverURL)
		}},
	}
	for _, tc := range cases {
		_, screen := newScene(10, 3)
		wantPanic(t, tc.name, func() { tc.draw(screen) })
	}

	t.Cleanup(func() { panicOnOverlap = true })
	panicOnOverlap = false
	want := []string{"  first", " <0>AAAA\n <1>BBBB", "\n    first", " <0>AAAA\n <1>BBBB"}
	for i, tc := range cases {
		s, screen := newScene(10, 3)
		tc.draw(screen)
		if got := s.compose(&coverLines); got != want[i] {
			t.Errorf("%s: composed %q, want %q", tc.name, got, want[i])
		}
	}
}

// A scene keeps its rows from frame to frame, and leaves out the rows
// after the last drawn.
func TestSceneReset(t *testing.T) {
	s, screen := newScene(10, 2)
	screen.text(0, 0, "a", paint{})
	screen.hit(image.Rect(0, 0, 1, 1), hit{kind: hitTab})
	screen.cover(image.Rect(2, 0, 4, 2), slotBar, coverURL)
	screen = s.reset(10, 4, s.cells)
	if len(s.hits) != 0 || len(s.covers) != 0 || composed(s) != "" {
		t.Errorf("reset kept %d hits, %d covers, %q", len(s.hits), len(s.covers), composed(s))
	}
	screen.text(0, 1, "b", paint{})
	if got := composed(s); got != "\nb" {
		t.Errorf("composed %q, want the rows to the last drawn", got)
	}
	if screen = s.reset(-1, -1, s.cells); screen.bounds != (image.Rectangle{}) || composed(s) != "" {
		t.Errorf("a screen of no size is %v, %q", screen.bounds, composed(s))
	}
}
