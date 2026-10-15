package theme

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// seeds range over what users pick, unreasonable ones too.
var seeds = map[string]Seed{
	"zero":        {},
	"blue":        {Accent: Oklch(0.72, 0.14, 250), Base: Oklch(0.16, 0.03, 265)},
	"black":       {Accent: ui.Hex("#FF0000"), Base: ui.Hex("#000000")},
	"light base":  {Accent: Oklch(0.6, 0.2, 30), Base: ui.Hex("#F4F4F4")},
	"loud base":   {Base: Oklch(0.2, 0.3, 140)},
	"dark accent": {Accent: ui.Hex("#101060")},
	"pale accent": {Accent: ui.Hex("#FFFFF0")},
	"dark text":   {Text: ui.Hex("#202020")},
	"wide accent": {Accent: ui.Oklch(0.7, 0.37, 145)},
}

// Whatever the seed, a derived theme is dark, inside sRGB, and even its
// Faint text keeps 4.5:1 on every surface.
func TestDeriveLegible(t *testing.T) {
	for name, seed := range seeds {
		t.Run(name, func(t *testing.T) {
			variants := map[string]Theme{"standard": Derive(seed), "high contrast": Derive(seed).HighContrast()}
			for variant, th := range variants {
				if th != th.SRGB() {
					t.Errorf("%s: holds wide colors", variant)
				}
				c := th.Color
				for _, fg := range []ui.Color{c.Text, c.Muted, c.Faint} {
					for _, bg := range []ui.Color{c.Base, c.Panel, c.Raised} {
						if r := contrastRatio(fg, bg); r < 4.5 {
							t.Errorf("%s: %s on %s is %.2f:1", variant, Hex(fg), Hex(bg), r)
						}
					}
				}
				if r := contrastRatio(c.Muted, c.Inverse); r < 4.5 {
					t.Errorf("%s: toasts' lines %.2f:1", variant, r)
				}
				if r := contrastRatio(c.Accent, c.Raised); r < 3 {
					t.Errorf("%s: accent on raised %.2f:1", variant, r)
				}
			}
			th := Derive(seed)
			if l, _, _ := lch(th.Color.Base); l > maxBaseL+0.01 {
				t.Errorf("base lightness %.3f, over %v", l, maxBaseL)
			}
			c := th.Color
			for i, s := range []ui.Color{c.Base, c.Panel, c.Raised, c.Hover, c.Pressed} {
				if i > 0 {
					prev := []ui.Color{c.Base, c.Panel, c.Raised, c.Hover}[i-1]
					if luminance(s) <= luminance(prev) {
						t.Errorf("surface %d (%s) not lighter than %s", i, Hex(s), Hex(prev))
					}
				}
			}
			if th.Layout != Default().Layout || th.Type != Default().Type {
				t.Error("Derive changed what colors are not")
			}
		})
	}
}

// A light base keeps its hue.
func TestDeriveDarkensLightBase(t *testing.T) {
	seed := Oklch(0.95, 0.04, 90)
	th := Derive(Seed{Base: seed})
	l, _, h := lch(th.Color.Base)
	if l > maxBaseL+0.01 {
		t.Errorf("base lightness %.3f", l)
	}
	if _, _, want := lch(seed); h < want-5 || h > want+5 {
		t.Errorf("base hue %.0f, want %.0f", h, want)
	}
}

// Zero seeds take Default's; a legible seed text is kept.
func TestDeriveSeeds(t *testing.T) {
	d := Default()
	if got := Derive(Seed{}); got.Color.Accent != d.Color.Accent || got.Color.Base != d.Color.Base {
		t.Errorf("zero seed: accent %s, base %s", Hex(got.Color.Accent), Hex(got.Color.Base))
	}
	text := ui.Hex("#F0E0D0")
	if got := Derive(Seed{Text: text}); got.Color.Text != text || got.Color.InverseText != text {
		t.Errorf("text %s, want the seed's %s", Hex(got.Color.Text), Hex(text))
	}
	pale := Derive(Seed{Accent: ui.Hex("#FFFFF0")})
	if pale.Color.AccentText == white {
		t.Error("white text on a pale accent")
	}
}

func TestPresets(t *testing.T) {
	ps := Presets()
	if len(ps) < 3 || ps[0].ID != DefaultID || ps[0].Theme() != Default() {
		t.Fatalf("presets %v: want Default first, then others", ps)
	}
	seen := map[string]bool{}
	for _, p := range ps {
		if p.ID == "" || p.Name == "" || seen[p.ID] {
			t.Errorf("preset %q %q: empty or repeated", p.ID, p.Name)
		}
		seen[p.ID] = true
		th := p.Theme()
		if th != th.SRGB() || th.Layout != th.Layout.Normalized() {
			t.Errorf("%s: wide colors or a layout not normal", p.ID)
		}
		if _, err := readable(th); err != nil {
			t.Errorf("%s: %v", p.ID, err)
		}
		if got, ok := PresetByID(p.ID); !ok || got.Name != p.Name {
			t.Errorf("PresetByID(%q) = %v, %v", p.ID, got.Name, ok)
		}
	}
	if _, ok := PresetByID("nope"); ok {
		t.Error("found a preset that is not")
	}
}

func TestLCH(t *testing.T) {
	for _, want := range [][3]float32{{0.68, 0.2, 33}, {0.5, 0.1, 250}, {0.9, 0.05, 120}} {
		l, c, h := lch(Oklch(want[0], want[1], want[2]))
		if abs(l-want[0]) > 0.01 || abs(c-want[1]) > 0.01 || abs(h-want[2]) > 1.5 {
			t.Errorf("lch(Oklch%v) = %v %v %v", want, l, c, h)
		}
	}
}

func abs(v float32) float32 { return max(v, -v) }
