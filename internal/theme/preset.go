package theme

// Preset is a built-in theme. Settings keep its ID, so IDs never change;
// Name is what the user sees.
type Preset struct {
	ID, Name string
	build    func() Theme
}

// Theme makes the preset's theme anew on each call.
func (p Preset) Theme() Theme { return p.build() }

// DefaultID is the ID of Default's preset.
const DefaultID = "default"

var presets = []Preset{
	{DefaultID, "Default", Default},
	{"midnight", "Midnight", func() Theme {
		return Derive(Seed{Accent: Oklch(0.72, 0.14, 250), Base: Oklch(0.16, 0.03, 265)})
	}},
	{"forest", "Forest", func() Theme {
		return Derive(Seed{Accent: Oklch(0.76, 0.15, 152), Base: Oklch(0.16, 0.025, 165)})
	}},
	{"orchid", "Orchid", func() Theme {
		return Derive(Seed{Accent: Oklch(0.7, 0.18, 340), Base: Oklch(0.16, 0.03, 320)})
	}},
	// Denser, for long sessions at a desk: more songs per screen, and the
	// player bar on top.
	{"studio", "Studio", func() Theme {
		t := Derive(Seed{Accent: Oklch(0.8, 0.14, 75), Base: Oklch(0.15, 0.006, 70)})
		t.Layout.Density, t.Layout.PlayerBar = Compact, Top
		return t
	}},
}

// Presets returns the built-in themes, Default first. The slice is shared:
// callers must not change it.
func Presets() []Preset { return presets }

// PresetByID returns the preset with id.
func PresetByID(id string) (Preset, bool) {
	for _, p := range presets {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}
