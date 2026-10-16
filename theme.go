package main

import (
	"runtime"
	"strings"
	"time"
	"yt-music-player/internal/theme"
	"yt-music-player/locales"

	"github.com/egoist/mygo/ui"
)

// th is the current pass's theme, set by applyTheme. A global, because
// widgets are free functions that read tokens without a Context.
var th = new(theme.Default())

var colorFade = ui.ElementTransition{Colors: true, Duration: 140 * time.Millisecond}

// over composites top over base (Porter-Duff "over"), both possibly
// translucent, so a state tint can sit on a control's own fill.
func over(top, base ui.Color) ui.Color {
	ta, ba := float32(top.A)/255, float32(base.A)/255
	a := ta + ba*(1-ta)
	if a == 0 {
		return ui.Transparent
	}
	mix := func(t, b uint8) uint8 { return uint8((float32(t)*ta+float32(b)*ba*(1-ta))/a + 0.5) }
	return ui.RGBA(mix(top.R, base.R), mix(top.G, base.G), mix(top.B, base.B), a)
}

// setTheme makes the High Contrast variant once, not every pass. Views
// rely on a normalized layout.
func (a *app) setTheme(t theme.Theme) {
	a.theme = t.SRGB()
	a.theme.Layout = a.theme.Layout.Normalized()
	a.contrast = a.theme.HighContrast()
}

func (a *app) applyTheme(c *ui.Context) {
	th = &a.theme
	if c.Preferences().HighContrast {
		th = &a.contrast
	}
	col := &th.Color
	// c.Theme() is this pass's own copy, so it changes in place instead
	// of being copied to the heap.
	t := c.Theme()
	t.Dark = true
	t.Background = col.Panel
	t.Surface = col.Raised
	t.SurfaceHover = col.Hover
	t.SurfacePressed = col.Pressed
	t.Border = col.Line
	t.Text = col.Text
	t.TextMuted = col.Muted
	t.Accent = col.Accent
	t.AccentHover = col.AccentHover
	t.AccentPressed = col.AccentPressed
	t.AccentText = col.AccentText
	t.Focus = col.Focus
	t.Selection = col.Selection
	t.Inverse = col.Inverse
	t.InverseText = col.InverseText
	t.Scrollbar = col.Scrollbar
	t.Radius = th.Radius.Control
	c.SetTheme(t)
}

func eyebrow(c *ui.Context, s string) ui.Element {
	return ui.Text(c, upper(s)).FontSize(th.Type.Caption).FontWeight(600).LetterSpacing(1.4).
		TextColor(th.Color.Muted).SingleLine()
}

var uppers = map[string]string{}

// upper caches strings.ToUpper, which allocates for any lowercase letter:
// eyebrows build every frame.
func upper(s string) string {
	if u, ok := uppers[s]; ok {
		return u
	}
	// Shelf titles change with each feed; clearing bounds the map.
	if len(uppers) >= 256 {
		clear(uppers)
	}
	u := strings.ToUpper(s)
	uppers[s] = u
	return u
}

// heading's level ranks it for assistive technology: 1 for a page, 2 for
// a section, 0 for plain text, as inside a button, which is read as one.
func heading(c *ui.Context, s string, size float32, level int) ui.Element {
	h := ui.Text(c, s).FontSize(size).FontWeight(700).LetterSpacing(-size * 0.022).TextColor(th.Color.Text)
	if level > 0 {
		h.Role(ui.RoleHeading).Level(level)
	}
	return h
}

// tip shows text by e while hovered or focused. Unlike Tooltip it is not
// read out, as it mostly repeats the label; keys, a shortcut, is read
// instead and shown after text.
func tip(c *ui.Context, e ui.Element, text, keys string) {
	if keys != "" {
		e.Description(keys)
	}
	t := c.Theme()
	ui.TooltipBase(c, e, func(bubble ui.Element) {
		// Same look as ui.Tooltip.
		bubble.MaxWidth(t.Space(80)).Padding(t.Space(1.25), t.Space(2)).Radius(t.Space(1.25)).
			Background(t.Inverse).TextColor(t.InverseText).FontSize(t.FontSize-1).
			Shadow(0, 2, 8, 0, th.Color.Shadow.Alpha(0.2)).
			AttachTo(e, ui.AnchorTop, ui.AnchorBottom).Margin(0, 0, t.Space(1.5), 0)
		bubble.Children(func() {
			if keys != "" {
				text = locales.ShortcutHint(text, keys)
			}
			ui.Text(c, text)
		})
	})
}

// hints names the shortcuts as the platform's menus write them; Play's,
// the space bar, is a word: locales.KeySpace.
var hints = func() (h struct{ next, prev, search, back, forward, appearance string }) {
	if runtime.GOOS == "darwin" {
		h.next, h.prev, h.search = "⌘→", "⌘←", "⌘K"
		h.back, h.forward, h.appearance = "⌘[", "⌘]", "⌘,"
		return h
	}
	h.next, h.prev, h.search = "Ctrl+→", "Ctrl+←", "Ctrl+K"
	h.back, h.forward, h.appearance = "Alt+←", "Alt+→", "Ctrl+,"
	return h
}()

// oklch is theme.Oklch: ui.Oklch clamped to sRGB, which keeps the window
// out of float16 buffers.
func oklch(l, c, h float32) ui.Color { return theme.Oklch(l, c, h) }

func boolToFloat(b bool) float32 {
	if b {
		return 1
	}
	return 0
}
