package theme

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestParseExample(t *testing.T) {
	data, err := os.ReadFile("testdata/dusk.json")
	if err != nil {
		t.Fatal(err)
	}
	name, th, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if name != "Dusk" {
		t.Errorf("name %q", name)
	}
	seeded := Derive(Seed{Accent: ui.Hex("#7AA2F7"), Base: Oklch(0.17, 0.03, 275)})
	if th.Color.Accent != seeded.Color.Accent || th.Color.Panel != seeded.Color.Panel {
		t.Error("the seed's colors were not derived")
	}
	if th.Color.Rise != Oklch(0.8, 0.15, 160) || th.Color.Badge != ui.Hex("#FFFFFF24") {
		t.Errorf("overrides: rise %s, badge %v", Hex(th.Color.Rise), th.Color.Badge)
	}
	if th.Radius.Card != 8 || th.Radius.Large != 12 || th.Radius.Dialog != Default().Radius.Dialog {
		t.Errorf("radius %+v", th.Radius)
	}
	l := th.Layout
	// Normalized: the panel moved off the sidebar's side.
	isMoved := l.SidebarSide == Right && l.PanelSide == Left
	if !isMoved || l.Density != Compact || l.Panel != 360 || l.Sidebar != Default().Layout.Sidebar {
		t.Errorf("layout %+v", l)
	}
}

// A file of a few tokens keeps its base's others.
func TestParsePartial(t *testing.T) {
	for _, tc := range []struct {
		name, json string
		check      func(t *testing.T, th Theme)
	}{
		{"empty", `{}`, func(t *testing.T, th Theme) {
			if th != Default() {
				t.Error("an empty file is not Default")
			}
		}},
		{"base", `{"base": "studio"}`, func(t *testing.T, th Theme) {
			p, _ := PresetByID("studio")
			if th != p.Theme() {
				t.Error("not the base preset")
			}
		}},
		{"one color", `{"color": {"accentHover": "#123456"}}`, func(t *testing.T, th Theme) {
			want := Default()
			want.Color.AccentHover = ui.Hex("#123456")
			if th != want {
				t.Error("changed more than the accent's hover")
			}
		}},
		{"any case", `{"color": {"ACCENT": "#ff8800"}, "type": {"TITLE": 15}}`, func(t *testing.T, th Theme) {
			if th.Color.Accent != ui.Hex("#FF8800") || th.Type.Title != 15 {
				t.Errorf("accent %s, title %v", Hex(th.Color.Accent), th.Type.Title)
			}
		}},
		{"oklch", `{"color": {"line": "oklch(70% 0.1 200 / 0.5)"}, "contrast": {"focus": "oklch(0.7 0.4 30)"}}`, func(t *testing.T, th Theme) {
			if th.Color.Line != Oklch(0.7, 0.1, 200).Alpha(0.5) {
				t.Errorf("line %v", th.Color.Line)
			}
			// Wide, clamped into sRGB.
			if th.Contrast.Focus != ui.Oklch(0.7, 0.4, 30).SRGB() {
				t.Errorf("focus %v", th.Contrast.Focus)
			}
		}},
		{"numbers", `{"type": {"title": 15}, "unit": 5, "radius": {"pill": 999}, "layout": {"sizes": {"row": 64}, "regions": {"gutter": 0}}}`, func(t *testing.T, th Theme) {
			l := th.Layout
			if th.Type.Title != 15 || th.Space(2) != 10 || th.Radius.Pill != 999 || l.Sizes.Row != 64 || l.Gutter != 0 {
				t.Errorf("title %v, unit %v, pill %v, row %v, gutter %v", th.Type.Title, th.Unit, th.Radius.Pill, l.Sizes.Row, l.Gutter)
			}
		}},
		{"layout", `{"layout": {"sidebar": "hidden", "panel": "Left", "playerBar": "top", "density": "compact"}}`, func(t *testing.T, th Theme) {
			l := th.Layout
			if l.SidebarSide != Hidden || l.PanelSide != Left || l.PlayerBar != Top || l.Density != Compact {
				t.Errorf("layout %+v", l)
			}
		}},
		// Too dark to read, lightened to 4.5:1.
		{"dim text", `{"color": {"faint": "#303030", "text": "#404040"}}`, func(t *testing.T, th Theme) {
			for _, fg := range []ui.Color{th.Color.Text, th.Color.Faint} {
				if r := contrastRatio(fg, th.Color.Raised); r < 4.5 {
					t.Errorf("%s on raised %.2f:1", Hex(fg), r)
				}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, th, err := Parse([]byte(tc.json))
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, th)
		})
	}
}

// Errors say where the mistake is.
func TestParseErrors(t *testing.T) {
	for _, tc := range []struct{ name, json, want string }{
		{"not json", `{`, "unexpected EOF"},
		{"trailing", `{} {}`, "after top-level value"},
		{"unknown field", `{"colour": {}}`, `unknown object member name "colour"`},
		{"unknown seed field", `{"seed": {"hue": "#fff"}}`, "seed.hue: no such token"},
		{"unknown layout field", `{"layout": {"width": 3}}`, `unknown object member name "width"`},
		{"unknown color", `{"color": {"chartreuse": "#fff"}}`, "color.chartreuse: no such token"},
		{"bad hex", `{"color": {"accent": "#12345"}}`, `color.accent: invalid color "#12345"`},
		{"bad digits", `{"color": {"accent": "#GGHHII"}}`, `color.accent: invalid color`},
		{"bad oklch", `{"contrast": {"line": "oklch(0.5 0.1)"}}`, "contrast.line: invalid color"},
		{"oklch out of range", `{"color": {"text": "oklch(2 0.1 30)"}}`, "L is 0 to 1"},
		{"named color", `{"color": {"text": "white"}}`, `invalid color "white"`},
		{"bad seed", `{"seed": {"accent": "red"}}`, "seed.accent: invalid color"},
		{"unknown base", `{"base": "solarized"}`, `base: no preset "solarized"`},
		{"type too small", `{"type": {"title": 2}}`, "type.title: 2 is out of 6 to 96"},
		{"unit too large", `{"unit": 9}`, "unit: 9 is out of 2 to 8"},
		{"unknown type", `{"type": {"huge": 20}}`, "type.huge: no such token"},
		{"row too tall", `{"layout": {"sizes": {"row": 500}}}`, "layout.sizes.row: 500 is out of 32 to 120"},
		{"unknown region", `{"layout": {"regions": {"titleBar": 40}}}`, "layout.regions.titleBar: no such token"},
		{"not a region", `{"layout": {"regions": {"sidebarSide": 1}}}`, "layout.regions.sidebarSide: no such token"},
		{"bad side", `{"layout": {"sidebar": "middle"}}`, `layout: sidebar: "middle" is none of left, right, hidden`},
		{"bad density", `{"layout": {"density": "cozy"}}`, "density"},
		{"number as string", `{"type": {"title": "14"}}`, `JSON string into Go float64 within "/type/title"`},
		{"translucent surface", `{"color": {"panel": "#13131680"}}`, "color.panel: must be opaque"},
		{"light surfaces", `{"color": {"base": "#FFFFFF", "panel": "#FFFFFF", "raised": "#FFFFFF"}}`, "cannot keep 4.5:1"},
		{"too large", `{"name": "` + strings.Repeat("a", MaxFileSize) + `"}`, "larger than"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Parse([]byte(tc.json))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v, want one with %q", err, tc.want)
			}
		})
	}
}

func TestEncodeRoundTrip(t *testing.T) {
	themes := map[string]Theme{"high contrast": Default().HighContrast()}
	for _, p := range Presets() {
		themes[p.ID] = p.Theme()
	}
	for name, th := range themes {
		data, err := Encode(name, th)
		if err != nil {
			t.Fatal(err)
		}
		got, back, err := Parse(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != name || back != th {
			t.Errorf("%s: read back differs\n%s", name, data)
		}
	}
}

func TestArrangement(t *testing.T) {
	d := Default().Layout
	if a := ArrangementOf(d); a != (Arrangement{"left", "right", "bottom", "comfortable"}) {
		t.Errorf("default arrangement %+v", a)
	}
	l, err := Arrangement{}.Apply(d)
	if err != nil || l != d {
		t.Errorf("empty arrangement changed %+v, %v", l, err)
	}
	l, err = Arrangement{Sidebar: "right", PlayerBar: "sideways", Density: "compact"}.Apply(d)
	if err == nil || !strings.Contains(err.Error(), "playerBar") {
		t.Errorf("error %v", err)
	}
	// Readable fields are applied, then normalized.
	if l.SidebarSide != Right || l.PanelSide != Left || l.PlayerBar != Bottom || l.Density != Compact {
		t.Errorf("layout %+v", l)
	}
	if got := ArrangementOf(l); got.Sidebar != "right" || got.Panel != "left" {
		t.Errorf("arrangement %+v", got)
	}
	if Side(9).String() != "9" {
		t.Error("an unknown side's name")
	}
}

func TestParseColor(t *testing.T) {
	for in, want := range map[string]ui.Color{
		"#fff":                     ui.RGB(255, 255, 255),
		" #102030 ":                ui.RGB(16, 32, 48),
		"#10203080":                {R: 16, G: 32, B: 48, A: 128},
		"OKLCH(0.5 0.1 250)":       Oklch(0.5, 0.1, 250),
		"oklch(50% 0.1 250 / 0.5)": Oklch(0.5, 0.1, 250).Alpha(0.5),
	} {
		got, err := parseColor(in)
		if err != nil || got != want {
			t.Errorf("parseColor(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "#", "#1234", "fff", "rgb(1,2,3)", "oklch(a b c)", "oklch(0.5 0.1 30 / 2)", "oklch(0.5 0.1 NaN)", "oklch(0.5 0.1 30 / NaN)"} {
		if _, err := parseColor(in); err == nil {
			t.Errorf("parseColor(%q) took it", in)
		}
	}
	for _, c := range []ui.Color{ui.RGB(1, 2, 3), {R: 1, G: 2, B: 3, A: 4}} {
		if got, err := parseColor(formatColor(c)); err != nil || got != c {
			t.Errorf("%v read back as %v, %v", c, got, err)
		}
	}
}

// Only exported float fields take numbers; others are no tokens, never a
// panic.
func TestSetNumbersOnlyIntoFloats(t *testing.T) {
	var group struct {
		Size   float32
		Count  int
		hidden float32
	}
	b := bounds{"": {0, 10}}
	if err := set("group", &group, map[string]float64{"size": 4}, b.put); err != nil || group.Size != 4 {
		t.Errorf("size: %v, set to %v", err, group.Size)
	}
	for _, token := range []string{"count", "hidden"} {
		if err := set("group", &group, map[string]float64{token: 1}, b.put); !errors.Is(err, errNoToken) {
			t.Errorf("%s: err %v, want errNoToken", token, err)
		}
	}
	_ = group.hidden
}
