package tui

import (
	"image"
	"testing"
)

func TestLayout(t *testing.T) {
	rows := func(w, y0, y1 int) image.Rectangle { return band(w, y0, y1-y0) }
	for _, tc := range []struct {
		name string
		in   layoutIn
		want frameRects
	}{
		{"80x24", layoutIn{w: 80, h: 24, tabsRows: 2, barRows: 4}, frameRects{
			tabs: rows(80, 0, 2), body: rows(80, 2, 20), banner: rows(80, 20, 20), bar: rows(80, 20, 24),
		}},
		{"80x24-banner", layoutIn{w: 80, h: 24, hasBanner: true, tabsRows: 2, barRows: 4}, frameRects{
			tabs: rows(80, 0, 2), body: rows(80, 2, 19), banner: rows(80, 19, 20), bar: rows(80, 20, 24),
		}},
		{"one-body-line", layoutIn{w: 40, h: 7, tabsRows: 2, barRows: 4}, frameRects{
			tabs: rows(40, 0, 2), body: rows(40, 2, 3), banner: rows(40, 3, 3), bar: rows(40, 3, 7),
		}},
		// No body: the bar from the top, the lines under it empty.
		{"no-body", layoutIn{w: 80, h: 6, tabsRows: 2, barRows: 4}, frameRects{
			banner: rows(80, 0, 0), bar: rows(80, 0, 4),
		}},
		{"only-bar", layoutIn{w: 80, h: 4, tabsRows: 2, barRows: 4}, frameRects{
			banner: rows(80, 0, 0), bar: rows(80, 0, 4),
		}},
		// Too short: the bar's last lines, its top above the screen.
		{"bar-cut", layoutIn{w: 80, h: 2, tabsRows: 2, barRows: 4}, frameRects{
			banner: rows(80, -2, -2), bar: rows(80, -2, 2),
		}},
		{"banner-cut", layoutIn{w: 80, h: 3, hasBanner: true, tabsRows: 2, barRows: 4}, frameRects{
			banner: rows(80, -2, -1), bar: rows(80, -1, 3),
		}},
		{"1x1", layoutIn{w: 1, h: 1, tabsRows: 2, barRows: 4}, frameRects{
			banner: rows(1, -3, -3), bar: rows(1, -3, 1),
		}},
		{"0x0", layoutIn{tabsRows: 2, barRows: 4}, frameRects{
			banner: rows(0, -4, -4), bar: rows(0, -4, 0),
		}},
		// The parts' own rows move the others.
		{"bar-of-5", layoutIn{w: 80, h: 24, hasBanner: true, tabsRows: 2, barRows: 5}, frameRects{
			tabs: rows(80, 0, 2), body: rows(80, 2, 18), banner: rows(80, 18, 19), bar: rows(80, 19, 24),
		}},
		{"tabs-of-1", layoutIn{w: 30, h: 6, tabsRows: 1, barRows: 4}, frameRects{
			tabs: rows(30, 0, 1), body: rows(30, 1, 2), banner: rows(30, 2, 2), bar: rows(30, 2, 6),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := layout(tc.in); got != tc.want {
				t.Errorf("layout = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// Every layout stacks its parts, keeps the bar's rows and ends the bar on
// the last row when the body shows; what shows is on the screen, apart.
func TestLayoutInvariants(t *testing.T) {
	for _, w := range []int{0, 1, 20, 43, 44, 80, 200} {
		for h := range 30 {
			for _, banner := range []bool{false, true} {
				for _, sizes := range [][2]int{{2, 4}, {1, 4}, {2, 5}, {3, 2}} {
					in := layoutIn{w: w, h: h, hasBanner: banner, tabsRows: sizes[0], barRows: sizes[1]}
					checkLayout(t, in, layout(in))
				}
			}
		}
	}
}

func checkLayout(t *testing.T, in layoutIn, r frameRects) {
	t.Helper()
	bannerRows := 0
	if in.hasBanner {
		bannerRows = 1
	}
	stack := bannerRows + in.barRows
	switch {
	case r.bar.Dy() != in.barRows || r.banner.Dy() != bannerRows:
		t.Errorf("%+v: bar %v, banner %v", in, r.bar, r.banner)
	case r.banner.Max.Y != r.bar.Min.Y:
		t.Errorf("%+v: banner %v not over bar %v", in, r.banner, r.bar)
	case r.body.Dy() == 0 && (r.tabs.Dy() != 0 || r.bar.Max.Y != min(in.h, stack)):
		t.Errorf("%+v: without a body, tabs %v, bar %v", in, r.tabs, r.bar)
	case r.body.Dy() > 0 && (r.tabs != band(in.w, 0, in.tabsRows) || r.body.Min.Y != r.tabs.Max.Y ||
		r.banner.Min.Y != r.body.Max.Y || r.bar.Max.Y != in.h || r.body.Dy() < minBody):
		t.Errorf("%+v: tabs %v, body %v, banner %v, bar %v", in, r.tabs, r.body, r.banner, r.bar)
	}
	screen := image.Rect(0, 0, in.w, in.h)
	parts := []image.Rectangle{r.tabs, r.body, r.banner, r.bar}
	for i, a := range parts {
		a = a.Intersect(screen)
		if !a.In(screen) {
			t.Errorf("%+v: part %d shows %v", in, i, a)
		}
		for _, b := range parts[i+1:] {
			if a.Overlaps(b.Intersect(screen)) {
				t.Errorf("%+v: %v overlaps %v", in, a, b)
			}
		}
	}
}

// A cover goes from the padding, its text a gap after it; without one,
// the text starts at the padding.
func TestLayBeside(t *testing.T) {
	for _, tc := range []struct {
		y, cols, rows int
		want          beside
	}{
		{1, 4, 2, beside{cover: image.Rect(padX, 1, padX+4, 3), textX: padX + 4 + gapX}},
		{0, 6, 3, beside{cover: image.Rect(padX, 0, padX+6, 3), textX: padX + 6 + gapX}},
		{1, 0, 2, beside{textX: padX}},
		{1, 4, 0, beside{textX: padX}},
	} {
		if got := layBeside(tc.y, tc.cols, tc.rows); got != tc.want {
			t.Errorf("layBeside(%d, %d, %d) = %+v, want %+v", tc.y, tc.cols, tc.rows, got, tc.want)
		}
	}
}
