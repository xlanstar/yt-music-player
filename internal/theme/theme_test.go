package theme

import (
	"reflect"
	"testing"

	"github.com/egoist/mygo/ui"
)

// Pins Default's hand-tuned look.
func TestDefault(t *testing.T) {
	d := Default()
	accent := ui.Oklch(0.68, 0.2, 33).SRGB()
	white := ui.RGB(255, 255, 255)
	for name, gotWant := range map[string][2]ui.Color{
		"Base":        {d.Color.Base, ui.Hex("#09090B")},
		"Panel":       {d.Color.Panel, ui.Hex("#131316")},
		"Raised":      {d.Color.Raised, ui.Hex("#1C1C21")},
		"Text":        {d.Color.Text, ui.Hex("#F5F2ED")},
		"Muted":       {d.Color.Muted, ui.Hex("#A19DA7")},
		"Faint":       {d.Color.Faint, ui.Hex("#8E8B94")},
		"Line":        {d.Color.Line, white.Alpha(0.07)},
		"Focus":       {d.Color.Focus, accent.Alpha(0.8)},
		"Accent":      {d.Color.Accent, accent},
		"AccentHover": {d.Color.AccentHover, accent.Mix(white, 0.1)},
		"RowHover":    {d.Color.RowHover, white.Alpha(0.05)},
		"Inverse":     {d.Color.Inverse, ui.Hex("#2B2B31")},
	} {
		if gotWant[0] != gotWant[1] {
			t.Errorf("%s = %v, want %v", name, gotWant[0], gotWant[1])
		}
	}
	l := d.Layout
	if l.Sidebar != 248 || l.Panel != 340 || l.Player != 88 || l.TitleBar != 52 ||
		l.Header != 68 || l.Gutter != 8 || l.Page != 28 || l.PanelRadius != 16 {
		t.Errorf("layout %+v", l)
	}
	if l.SidebarSide != Left || l.PanelSide != Right || l.PlayerBar != Bottom || l.Density != Comfortable ||
		l.Sizes != (Sizes{Row: 58, RowArt: 48, Card: 180, SmallCard: 168, Cell: 170}) {
		t.Errorf("arrangement %+v", l)
	}
	if d.Space(1.5) != 6 || d.Radius.Control != 10 || d.Type.Page != 34 {
		t.Errorf("scales: unit %v, radius %v, page title %v", d.Unit, d.Radius.Control, d.Type.Page)
	}
}

func TestHighContrast(t *testing.T) {
	d := Default()
	hc := d.HighContrast()
	if hc.Color.Line != d.Contrast.Line || hc.Color.Muted != d.Contrast.Muted ||
		hc.Color.Faint != d.Contrast.Faint || hc.Color.Focus != d.Contrast.Focus {
		t.Error("High Contrast kept the standard colors")
	}
	if hc.Color.Text != d.Color.Text || hc.Color.Panel != d.Color.Panel {
		t.Error("High Contrast changed what it does not strengthen")
	}
	if hc.HighContrast() != hc {
		t.Error("High Contrast twice differs from once")
	}
}

func TestTextContrast(t *testing.T) {
	for name, th := range map[string]Theme{"standard": Default(), "high contrast": Default().HighContrast()} {
		c := th.Color
		for _, fg := range []ui.Color{c.Text, c.Muted, c.Faint} {
			for _, bg := range []ui.Color{c.Base, c.Panel, c.Raised} {
				if r := contrastRatio(fg, bg); r < 4.5 {
					t.Errorf("%s: %v on %v is %.2f:1, want 4.5:1", name, fg, bg, r)
				}
			}
		}
	}
}

func TestSRGB(t *testing.T) {
	if d := Default(); d != d.SRGB() {
		t.Error("the default theme holds wide colors")
	}
	if hc := Default().HighContrast(); hc != hc.SRGB() {
		t.Error("the high contrast theme holds wide colors")
	}
	wide := ui.Oklch(0.7, 0.3, 30)
	if wide == wide.SRGB() {
		t.Fatal("test color is inside sRGB")
	}
	var th Theme
	th.Color.Accent, th.Contrast.Focus = wide, wide
	got := th.SRGB()
	if got.Color.Accent != wide.SRGB() || got.Contrast.Focus != wide.SRGB() {
		t.Error("SRGB left a wide color")
	}
	if Oklch(0.7, 0.3, 30) != wide.SRGB() {
		t.Error("Oklch returned a wide color")
	}
}

// colors must reach every color, or SRGB would let one through.
func TestColorsReachEveryColor(t *testing.T) {
	want := 0
	for _, v := range []any{Colors{}, Contrast{}} {
		typ := reflect.TypeOf(v)
		for field := range typ.Fields() {
			if field.Type == reflect.TypeFor[ui.Color]() {
				want++
			}
		}
	}
	var th Theme
	seen := map[*ui.Color]bool{}
	for _, c := range th.colors() {
		seen[c] = true
	}
	if len(seen) != want {
		t.Errorf("colors reached %d colors, want %d", len(seen), want)
	}
}

func TestNormalized(t *testing.T) {
	d := Default().Layout
	if d.Normalized() != d {
		t.Error("the default layout is not normal")
	}
	for _, tc := range []struct {
		name                   string
		sidebar, panel         Side
		wantSidebar, wantPanel Side
	}{
		{"mirrored", Right, Left, Right, Left},
		{"both left", Left, Left, Left, Right},
		{"both right", Right, Right, Right, Left},
		{"no sidebar", Hidden, Left, Hidden, Left},
		{"no panel", Left, Hidden, Left, Hidden},
		{"neither", Hidden, Hidden, Hidden, Hidden},
		{"out of range", 7, 9, Left, Right},
	} {
		l := d
		l.SidebarSide, l.PanelSide = tc.sidebar, tc.panel
		got := l.Normalized()
		if got.SidebarSide != tc.wantSidebar || got.PanelSide != tc.wantPanel {
			t.Errorf("%s: sidebar %v, panel %v; want %v, %v",
				tc.name, got.SidebarSide, got.PanelSide, tc.wantSidebar, tc.wantPanel)
		}
		if got.Normalized() != got {
			t.Errorf("%s: normalizing twice differs from once", tc.name)
		}
	}
	l := d
	l.PlayerBar, l.Density = 5, 5
	if got := l.Normalized(); got.PlayerBar != d.PlayerBar || got.Density != d.Density {
		t.Errorf("out of range: player bar %v, density %v", got.PlayerBar, got.Density)
	}
	l.PlayerBar, l.Density = Top, Compact
	if got := l.Normalized(); got.PlayerBar != Top || got.Density != Compact {
		t.Error("Normalized changed a valid player bar or density")
	}
}

func TestSized(t *testing.T) {
	l := Default().Layout
	if l.Sized() != l.Sizes {
		t.Errorf("comfortable sizes %+v, want %+v", l.Sized(), l.Sizes)
	}
	l.Density = Compact
	got := l.Sized()
	want := Sizes{Row: 49, RowArt: 41, Card: 153, SmallCard: 143, Cell: 145}
	if got != want {
		t.Errorf("compact sizes %+v, want %+v", got, want)
	}
	if l.Sizes != Default().Layout.Sizes {
		t.Error("Sized changed the layout's sizes")
	}
}

func TestHex(t *testing.T) {
	if got := Hex(Default().Color.Base); got != "#09090B" {
		t.Errorf("Hex = %q", got)
	}
}
