// Package theme holds the player's design tokens: colors, type and radius
// scales, spacing unit, and layout. It knows nothing of the views.
package theme

import (
	"fmt"
	"math"

	"github.com/egoist/mygo/ui"
)

// Theme is a whole look.
type Theme struct {
	Color    Colors
	Contrast Contrast
	Type     Type
	// Unit is MyGo's spacing unit, in DIPs.
	Unit   float32
	Radius Radius
	Layout Layout
}

// Space returns n spacing units, as ui.Theme.Space(n).
func (t *Theme) Space(n float32) float32 { return t.Unit * n }

// Colors are the chrome's colors. The chrome stays dark so it does not
// compete with the art, which carries the color.
type Colors struct {
	// Surfaces, from the window behind the panels to a pressed control.
	Base, Panel, Raised, Hover, Pressed ui.Color
	// Text, and quieter text; even Faint keeps 4.5:1 on surfaces.
	Text, Muted, Faint ui.Color
	Line               ui.Color // dividers and outlines
	Focus              ui.Color // the keyboard focus ring

	// Accent is for what plays, and for primary actions.
	Accent, AccentHover, AccentPressed ui.Color
	AccentText                         ui.Color // text and icons on the accent
	Selection                          ui.Color // selected text
	// Inverse fills tooltips and toasts, InverseText their text.
	Inverse, InverseText ui.Color
	Scrollbar            ui.Color

	// Light is for text and icons over art, Shadow for shadows and
	// scrims; uses apply their own alpha.
	Light, Shadow ui.Color

	// Translucent washes for states, so they suit any panel tint.
	NavHover, RowHover, RowCurrent ui.Color
	GhostHover, GhostPressed       ui.Color // controls without a resting fill
	GhostBorder                    ui.Color // outline of controls without a fill
	Field                          ui.Color // resting fill of chips and the search field
	Track                          ui.Color // the seek and volume bars' track
	Glass, GlassHover              ui.Color // buttons over art
	Badge                          ui.Color // small tags, as a top result's kind
	// LyricSung and LyricAhead are the lyrics around the line being sung.
	LyricSung, LyricAhead ui.Color

	// Rise and Fall are chart trends.
	Rise, Fall, Error ui.Color
	// Brand is the brand mark gradient's second stop, after Accent.
	Brand ui.Color
	// Releases, Charts and Moods tint Explore's tiles.
	Releases, Charts, Moods ui.Color
	// Drawn covers for art of no known kind.
	CoverFrom, CoverTo, CoverInk ui.Color
}

// Contrast are the colors High Contrast strengthens.
type Contrast struct {
	Line, Muted, Faint, Focus ui.Color
}

// Type is the scale of font sizes, in DIPs.
type Type struct {
	Micro   float32 // the brand's tagline, chart trends
	Badge   float32 // small tags
	Caption float32 // eyebrows, key hints, footnotes
	Time    float32 // the seek bar's times
	Small   float32 // compact lines, table heads, notes
	Sub     float32 // lines under titles
	Detail  float32 // durations, chips, albums in tables
	Label   float32 // buttons, compact titles, owners
	Title   float32 // song and card titles, navigation
	Lead    float32 // the artist under the panel's title
	Tile    float32 // Explore's tiles
	Brand   float32 // the app's name

	// Headings, from the smallest, and what shares their size.
	Minor      float32 // small sections, as search's; chart positions
	Message    float32 // a page with nothing else to show
	Section    float32 // shelves, dialogs, the panel's song, lyrics
	Feature    float32 // the top result
	Page       float32 // pages' titles
	HeroNarrow float32 // the hero in a narrow window
	Hero       float32
	Display    float32 // an album's or playlist's title
}

// Radius is the scale of corner radii, in DIPs. Circles need none.
type Radius struct {
	Thumb   float32 // covers in rows, key hints
	Cover   float32 // covers in the sidebar
	Small   float32 // the player bar's cover, small buttons
	Mark    float32 // the brand mark
	Control float32 // rows, navigation, tiles, toasts
	Card    float32 // cards' covers
	Sleeve  float32 // the hero's fanned covers
	Large   float32 // large covers and tiles
	Dialog  float32 // dialogs and the top result
	Hero    float32
	Pill    float32 // fully rounded ends
}

// Layout is how the window's regions are arranged, and their sizes in DIPs.
type Layout struct {
	// SidebarSide and PanelSide place the sidebar and the now-playing panel
	// beside the main panel (see Normalized); PlayerBar is the window edge
	// the player bar spans.
	SidebarSide, PanelSide Side
	PlayerBar              Edge
	// Density scales Sizes (see Sized).
	Density Density
	Sizes   Sizes

	Sidebar  float32 // width
	Panel    float32 // the now-playing panel's width
	Player   float32 // the player bar's height
	TitleBar float32 // the native title bar's height, which the view draws under
	// Header is the height of both the top bar and the panel's header, so
	// they line up.
	Header      float32
	Gutter      float32 // between the panels and around them
	Page        float32 // pages' side padding
	PanelRadius float32
}

// Side is where a region sits beside the main panel.
type Side uint8

// The sides; Hidden hides the region.
const (
	Left Side = iota
	Right
	Hidden
)

// Edge is an edge of the window.
type Edge uint8

// The edges a bar can span.
const (
	Bottom Edge = iota
	Top
)

// Density is how tightly songs and cards pack.
type Density uint8

// The densities, from the roomiest.
const (
	Comfortable Density = iota
	Compact
)

// compactScale fits about a sixth more songs while covers stay large
// enough to tell apart.
const compactScale = 0.85

// Sizes are the sizes of songs and cards, which Density scales.
type Sizes struct {
	Row       float32 // a song row's height in tables
	RowArt    float32 // a song's cover in rows outside tables
	Card      float32 // a card's width in Home's shelves
	SmallCard float32 // a card's width in other shelves
	Cell      float32 // the least width of a card in a grid
}

// Sized returns l's Sizes scaled by its Density, rounded to whole DIPs to
// stay on the pixel grid.
func (l *Layout) Sized() Sizes {
	if l.Density != Compact {
		return l.Sizes
	}
	sizes := l.Sizes
	for _, v := range [...]*float32{&sizes.Row, &sizes.RowArt, &sizes.Card, &sizes.SmallCard, &sizes.Cell} {
		*v = float32(math.Round(float64(*v * compactScale)))
	}
	return sizes
}

// Normalized returns l with out-of-range values set to Default's, and a
// panel on the sidebar's side moved to the other: two regions on one side
// would narrow the main panel, and the panel would slide in under the
// sidebar. The sidebar keeps its side, as it navigates.
func (l Layout) Normalized() Layout {
	if l.SidebarSide > Hidden {
		l.SidebarSide = Left
	}
	if l.PanelSide > Hidden {
		l.PanelSide = Right
	}
	if l.PlayerBar > Top {
		l.PlayerBar = Bottom
	}
	if l.Density > Compact {
		l.Density = Comfortable
	}
	if l.PanelSide == l.SidebarSide && l.PanelSide != Hidden {
		l.PanelSide = Left + Right - l.SidebarSide
	}
	return l
}

// Oklch is ui.Oklch clamped into sRGB. One wide color in a frame makes
// macOS draw the window into float16 buffers, twice the memory of 8-bit.
func Oklch(l, c, h float32) ui.Color { return ui.Oklch(l, c, h).SRGB() } //nolint:forbidigo // Clamped here.

// Hex formats c as "#RRGGBB", dropping alpha, as window options take it.
func Hex(c ui.Color) string { return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B) }

var (
	white = ui.RGB(255, 255, 255)
	black = ui.RGB(0, 0, 0)
)

// Default returns the player's look.
func Default() Theme {
	accent := Oklch(0.68, 0.2, 33)
	text := ui.Hex("#F5F2ED")
	fall := Oklch(0.68, 0.18, 25)
	return Theme{
		Color: Colors{
			Base:    ui.Hex("#09090B"),
			Panel:   ui.Hex("#131316"),
			Raised:  ui.Hex("#1C1C21"),
			Hover:   ui.Hex("#25252B"),
			Pressed: ui.Hex("#303037"),
			Text:    text,
			Muted:   ui.Hex("#A19DA7"),
			Faint:   ui.Hex("#8E8B94"),
			Line:    white.Alpha(0.07),
			Focus:   accent.Alpha(0.8),

			Accent:        accent,
			AccentHover:   accent.Mix(white, 0.1),
			AccentPressed: accent.Mix(black, 0.1),
			AccentText:    white,
			Selection:     accent.Alpha(0.35),
			Inverse:       ui.Hex("#2B2B31"),
			InverseText:   text,
			Scrollbar:     white.Alpha(0.18),

			Light:  white,
			Shadow: black,

			NavHover:     white.Alpha(0.04),
			RowHover:     white.Alpha(0.05),
			RowCurrent:   white.Alpha(0.025),
			GhostHover:   white.Alpha(0.06),
			GhostPressed: white.Alpha(0.1),
			GhostBorder:  white.Alpha(0.16),
			Field:        white.Alpha(0.07),
			Track:        white.Alpha(0.14),
			Glass:        white.Alpha(0.14),
			GlassHover:   white.Alpha(0.22),
			Badge:        white.Alpha(0.1),
			LyricSung:    white.Alpha(0.38),
			LyricAhead:   white.Alpha(0.58),

			Rise:     Oklch(0.78, 0.16, 150),
			Fall:     fall,
			Error:    fall,
			Brand:    Oklch(0.5, 0.22, 10),
			Releases: Oklch(0.72, 0.15, 200),
			Charts:   accent,
			Moods:    Oklch(0.75, 0.15, 110),

			CoverFrom: ui.Hex("#3A3A42"),
			CoverTo:   ui.Hex("#1C1C21"),
			CoverInk:  ui.Hex("#A19DA7"),
		},
		Contrast: Contrast{
			Line:  white.Alpha(0.24),
			Muted: ui.Hex("#D2CED7"),
			Faint: ui.Hex("#B3AFB8"),
			Focus: accent,
		},
		Type: Type{
			Micro: 9, Badge: 10.5, Caption: 11, Time: 11.5, Small: 12, Sub: 12.5,
			Detail: 13, Label: 13.5, Title: 14, Lead: 14.5, Tile: 15, Brand: 16,
			Minor: 18, Message: 20, Section: 22, Feature: 28, Page: 34,
			HeroNarrow: 38, Hero: 46, Display: 52,
		},
		Unit: 4,
		Radius: Radius{
			Thumb: 6, Cover: 7, Small: 8, Mark: 9, Control: 10, Card: 12,
			Sleeve: 14, Large: 16, Dialog: 18, Hero: 22, Pill: 999,
		},
		Layout: Layout{
			SidebarSide: Left, PanelSide: Right, PlayerBar: Bottom, Density: Comfortable,
			Sizes:   Sizes{Row: 58, RowArt: 48, Card: 180, SmallCard: 168, Cell: 170},
			Sidebar: 248, Panel: 340, Player: 88, TitleBar: 52, Header: 68,
			Gutter: 8, Page: 28, PanelRadius: 16,
		},
	}
}

// HighContrast returns t with its Contrast colors swapped in.
func (t Theme) HighContrast() Theme {
	c := &t.Color
	c.Line, c.Muted, c.Faint, c.Focus = t.Contrast.Line, t.Contrast.Muted, t.Contrast.Faint, t.Contrast.Focus
	return t
}

// SRGB returns t with every color clamped into sRGB, for themes not made
// with Oklch.
func (t Theme) SRGB() Theme {
	for _, c := range t.colors() {
		*c = c.SRGB()
	}
	return t
}

// colors returns every color of t; TestColorsReachEveryColor checks none
// is missed.
func (t *Theme) colors() [47]*ui.Color {
	c, k := &t.Color, &t.Contrast
	return [...]*ui.Color{
		&c.Base, &c.Panel, &c.Raised, &c.Hover, &c.Pressed,
		&c.Text, &c.Muted, &c.Faint, &c.Line, &c.Focus,
		&c.Accent, &c.AccentHover, &c.AccentPressed, &c.AccentText, &c.Selection,
		&c.Inverse, &c.InverseText, &c.Scrollbar, &c.Light, &c.Shadow,
		&c.NavHover, &c.RowHover, &c.RowCurrent, &c.GhostHover, &c.GhostPressed,
		&c.GhostBorder, &c.Field, &c.Track, &c.Glass, &c.GlassHover, &c.Badge,
		&c.LyricSung, &c.LyricAhead, &c.Rise, &c.Fall, &c.Error, &c.Brand,
		&c.Releases, &c.Charts, &c.Moods, &c.CoverFrom, &c.CoverTo, &c.CoverInk,
		&k.Line, &k.Muted, &k.Faint, &k.Focus,
	}
}
