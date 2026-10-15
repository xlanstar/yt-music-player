package theme

import (
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

// file is a theme file (docs/theme.md, testdata/dusk.json): a base preset,
// a Seed that derives the colors anew, then single tokens, each optional.
// Tokens are the fields of Seed, Colors, Contrast, Type, Radius, Sizes and
// Layout, in any case. Unknown fields, bad colors, out-of-bounds numbers
// and text that cannot keep 4.5:1 are rejected.
type file struct {
	Name     string             `json:"name,omitzero"`
	Base     string             `json:"base,omitzero"`
	Seed     map[string]string  `json:"seed,omitzero"`
	Color    map[string]string  `json:"color,omitzero"`
	Contrast map[string]string  `json:"contrast,omitzero"`
	Type     map[string]float64 `json:"type,omitzero"`
	Unit     *float64           `json:"unit,omitzero"`
	Radius   map[string]float64 `json:"radius,omitzero"`
	Layout   *layoutFile        `json:"layout,omitzero"`
}

type layoutFile struct {
	Arrangement
	Sizes   map[string]float64 `json:"sizes,omitzero"`
	Regions map[string]float64 `json:"regions,omitzero"`
}

// MaxFileSize bounds a theme file; a whole theme takes about 3 KB.
const MaxFileSize = 64 << 10

// bounds are the lowest and highest DIPs a file may set per field name,
// "" for any field: past them views overlap or lose content. Fields
// without bounds are not settable.
type bounds map[string][2]float64

var (
	unitBounds   = [2]float64{2, 8}
	typeBounds   = bounds{"": {6, 96}}
	radiusBounds = bounds{"": {0, 999}}
	cardBounds   = [2]float64{100, 320}
	sizeBounds   = bounds{
		"Row": {32, 120}, "RowArt": {24, 96},
		"Card": cardBounds, "SmallCard": cardBounds, "Cell": cardBounds,
	}
	// No TitleBar: the platform sets it.
	regionBounds = bounds{
		"Sidebar": {160, 400}, "Panel": {240, 520}, "Player": {64, 140}, "Header": {48, 120},
		"Gutter": {0, 32}, "Page": {8, 64}, "PanelRadius": {0, 40},
	}
)

// Parse reads a theme file into its name, possibly empty, and its theme,
// inside sRGB, normalized and legible.
func Parse(data []byte) (name string, t Theme, err error) {
	if len(data) > MaxFileSize {
		return "", Theme{}, fmt.Errorf("theme: larger than %d KB", MaxFileSize>>10)
	}
	var f file
	if err := json.Unmarshal(data, &f, json.RejectUnknownMembers(true)); err != nil {
		return "", Theme{}, fmt.Errorf("theme: %w", err)
	}
	if t, err = f.theme(); err != nil {
		return "", Theme{}, fmt.Errorf("theme: %w", err)
	}
	return f.Name, t, nil
}

func (f *file) theme() (Theme, error) {
	base := cmp.Or(f.Base, DefaultID)
	preset, ok := PresetByID(base)
	if !ok {
		return Theme{}, fmt.Errorf("base: no preset %q", base)
	}
	t := preset.Theme()
	if f.Seed != nil {
		seed := Seed{Accent: t.Color.Accent, Base: t.Color.Base}
		if err := set("seed", &seed, f.Seed, putColor); err != nil {
			return Theme{}, err
		}
		derived := Derive(seed)
		t.Color, t.Contrast = derived.Color, derived.Contrast
	}
	unit := float64(t.Unit)
	if f.Unit != nil {
		unit = *f.Unit
	}
	t.Unit = float32(unit)
	layout := cmp.Or(f.Layout, &layoutFile{})
	var layoutErr error
	t.Layout, layoutErr = layout.Apply(t.Layout)
	if err := cmp.Or(
		set("color", &t.Color, f.Color, putColor),
		set("contrast", &t.Contrast, f.Contrast, putColor),
		set("type", &t.Type, f.Type, typeBounds.put),
		wrap("unit", inBounds(unit, unitBounds)),
		set("radius", &t.Radius, f.Radius, radiusBounds.put),
		wrap("layout", layoutErr),
		set("layout.sizes", &t.Layout.Sizes, layout.Sizes, sizeBounds.put),
		set("layout.regions", &t.Layout, layout.Regions, regionBounds.put),
	); err != nil {
		return Theme{}, err
	}
	t = t.SRGB()
	t.Layout = t.Layout.Normalized()
	return readable(t)
}

// readable lightens text that would not keep 4.5:1 on the surfaces, or
// fails where even white would not: the player is dark.
func readable(t Theme) (Theme, error) {
	colors, contrast := &t.Color, &t.Contrast
	for _, opaque := range []struct {
		name string
		c    ui.Color
	}{
		{"base", colors.Base},
		{"panel", colors.Panel},
		{"raised", colors.Raised},
		{"text", colors.Text},
		{"muted", colors.Muted},
		{"faint", colors.Faint},
	} {
		if opaque.c.A != 255 {
			return Theme{}, fmt.Errorf("color.%s: must be opaque", opaque.name)
		}
	}
	surfaces := [...]ui.Color{colors.Base, colors.Panel, colors.Raised}
	for _, fg := range []*ui.Color{&colors.Text, &colors.Muted, &colors.Faint, &contrast.Muted, &contrast.Faint} {
		var ok bool
		if *fg, ok = lighten(*fg, legible, surfaces[:]...); !ok {
			return Theme{}, errors.New("color: text cannot keep 4.5:1 on the surfaces; " +
				"the player is dark, so base, panel and raised must be too")
		}
	}
	return t, nil
}

// field returns the field of struct v named key, in any case, and its name.
// Fields are indexed here and below: v.Fields() allocates each StructField
// (TestBudgetParse: 162 allocs became 612).
func field(v reflect.Value, key string) (reflect.Value, string) {
	for i := range v.NumField() {
		if name := v.Type().Field(i).Name; strings.EqualFold(name, key) {
			return v.Field(i), name
		}
	}
	return reflect.Value{}, ""
}

var errNoToken = errors.New("no such token")

// set puts each value of from into the field of the struct at ptr it
// names, in sorted order so errors are the same each run.
func set[V any](group string, ptr any, from map[string]V, put func(f reflect.Value, name string, v V) error) error {
	v := reflect.ValueOf(ptr).Elem()
	for _, token := range sorted(from) {
		err := errNoToken
		if f, name := field(v, token); f.IsValid() {
			err = put(f, name, from[token])
		}
		if err != nil {
			return fmt.Errorf("%s.%s: %w", group, token, err)
		}
	}
	return nil
}

func putColor(f reflect.Value, _, s string) error {
	c, err := parseColor(s)
	if err != nil {
		return err
	}
	p, ok := reflect.TypeAssert[*ui.Color](f.Addr())
	if !ok {
		return errNoToken
	}
	*p = c
	return nil
}

func (b bounds) put(f reflect.Value, name string, n float64) error {
	lim, ok := b.of(name)
	if !ok {
		return errNoToken
	}
	if err := inBounds(n, lim); err != nil {
		return err
	}
	f.SetFloat(n)
	return nil
}

// sorted allocates nothing for an empty m, as most groups of a file are.
func sorted[V any](m map[string]V) []string {
	if len(m) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(m))
}

func (b bounds) of(name string) (lim [2]float64, ok bool) {
	if lim, ok = b[name]; !ok {
		lim, ok = b[""]
	}
	return lim, ok
}

func wrap(at string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", at, err)
}

func inBounds(n float64, lim [2]float64) error {
	if n < lim[0] || n > lim[1] {
		return fmt.Errorf("%v is out of %v to %v", n, lim[0], lim[1])
	}
	return nil
}

// Encode writes t as a theme file with every token, which Parse reads back
// as t: a start for a theme of one's own. Keys are sorted, as people read,
// edit and compare the files.
func Encode(name string, t Theme) ([]byte, error) {
	unit := float64(t.Unit)
	return json.Marshal(file{
		Name:     name,
		Color:    colorsOf(t.Color),
		Contrast: colorsOf(t.Contrast),
		Type:     numbersOf(t.Type, typeBounds),
		Unit:     &unit,
		Radius:   numbersOf(t.Radius, radiusBounds),
		Layout: &layoutFile{
			Arrangement: ArrangementOf(t.Layout),
			Sizes:       numbersOf(t.Layout.Sizes, sizeBounds),
			Regions:     numbersOf(t.Layout, regionBounds),
		},
	}, json.Deterministic(true), jsontext.WithIndent("  "))
}

func fileKey(field string) string { return strings.ToLower(field[:1]) + field[1:] }

func colorsOf(group any) map[string]string {
	v := reflect.ValueOf(group)
	m := map[string]string{}
	for i := range v.NumField() {
		if c, ok := reflect.TypeAssert[ui.Color](v.Field(i)); ok {
			m[fileKey(v.Type().Field(i).Name)] = formatColor(c)
		}
	}
	return m
}

func numbersOf(group any, b bounds) map[string]float64 {
	v := reflect.ValueOf(group)
	m := map[string]float64{}
	for i := range v.NumField() {
		if _, ok := b.of(v.Type().Field(i).Name); ok {
			m[fileKey(v.Type().Field(i).Name)] = v.Field(i).Float()
		}
	}
	return m
}

// Arrangement is a layout's arrangement as files and settings write it:
// "left", "right" or "hidden" for the sidebar and the panel, "bottom" or
// "top" for the player bar, "comfortable" or "compact" for the density.
// An empty field keeps the layout's.
type Arrangement struct {
	Sidebar   string `json:"sidebar,omitzero"`
	Panel     string `json:"panel,omitzero"`
	PlayerBar string `json:"playerBar,omitzero"`
	Density   string `json:"density,omitzero"`
}

var (
	sideNames    = [...]string{Left: "left", Right: "right", Hidden: "hidden"}
	edgeNames    = [...]string{Bottom: "bottom", Top: "top"}
	densityNames = [...]string{Comfortable: "comfortable", Compact: "compact"}
)

// String returns the side's name in files and settings.
func (s Side) String() string { return nameOf(sideNames[:], int(s)) }

// String returns the edge's name in files and settings.
func (e Edge) String() string { return nameOf(edgeNames[:], int(e)) }

// String returns the density's name in files and settings.
func (d Density) String() string { return nameOf(densityNames[:], int(d)) }

func nameOf(names []string, i int) string {
	if i < len(names) {
		return names[i]
	}
	return strconv.Itoa(i)
}

// ArrangementOf returns l's arrangement with every field set.
func ArrangementOf(l Layout) Arrangement {
	return Arrangement{
		Sidebar:   l.SidebarSide.String(),
		Panel:     l.PanelSide.String(),
		PlayerBar: l.PlayerBar.String(),
		Density:   l.Density.String(),
	}
}

// Apply returns l arranged as a says, normalized. Fields it cannot read
// keep l's and are each reported in the error.
func (a Arrangement) Apply(l Layout) (Layout, error) {
	var errs []error
	pick := func(key, value string, names []string, to *uint8) {
		if value == "" {
			return
		}
		i := slices.Index(names, strings.ToLower(value))
		if i < 0 {
			errs = append(errs, fmt.Errorf("%s: %q is none of %s", key, value, strings.Join(names, ", ")))
			return
		}
		*to = uint8(i) //nolint:gosec // an index into names
	}
	pick("sidebar", a.Sidebar, sideNames[:], (*uint8)(&l.SidebarSide))
	pick("panel", a.Panel, sideNames[:], (*uint8)(&l.PanelSide))
	pick("playerBar", a.PlayerBar, edgeNames[:], (*uint8)(&l.PlayerBar))
	pick("density", a.Density, densityNames[:], (*uint8)(&l.Density))
	return l.Normalized(), errors.Join(errs...)
}
