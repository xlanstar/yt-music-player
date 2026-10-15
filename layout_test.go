package main

import (
	"math"
	"testing"
	"yt-music-player/internal/theme"

	"github.com/egoist/mygo/ui"
)

func arranged(change func(l *theme.Layout)) *app {
	a := idleApp()
	t := theme.Default()
	change(&t.Layout)
	a.setTheme(t)
	return a
}

func find(t *testing.T, tt *ui.Tester, s string) ui.Rect {
	t.Helper()
	r, ok := tt.Find(s)
	if !ok {
		t.Fatalf("no %q in %q", s, tt.Texts())
	}
	return r
}

// Each arrangement places the regions around a main panel that keeps its
// room, with the window controls on either side.
func TestLayoutArrangements(t *testing.T) {
	for _, tc := range []struct {
		name           string
		change         func(l *theme.Layout)
		sidebar, panel theme.Side
		isBarOnTop     bool
		// Whether the player bar offers the panel.
		hasLyrics, hasCover bool
	}{
		{"Default", func(*theme.Layout) {}, theme.Left, theme.Right, false, true, true},
		{"Mirrored", func(l *theme.Layout) {
			l.SidebarSide, l.PanelSide = theme.Right, theme.Left
		}, theme.Right, theme.Left, false, true, true},
		{"NoSidebar", func(l *theme.Layout) { l.SidebarSide = theme.Hidden }, theme.Hidden, theme.Right, false, true, true},
		{"NoPanel", func(l *theme.Layout) { l.PanelSide = theme.Hidden }, theme.Left, theme.Hidden, false, false, false},
		{"Neither", func(l *theme.Layout) {
			l.SidebarSide, l.PanelSide = theme.Hidden, theme.Hidden
		}, theme.Hidden, theme.Hidden, false, false, false},
		{"PlayerTop", func(l *theme.Layout) { l.PlayerBar = theme.Top }, theme.Left, theme.Right, true, true, true},
		{"MirroredTop", func(l *theme.Layout) {
			l.SidebarSide, l.PanelSide, l.PlayerBar = theme.Right, theme.Left, theme.Top
		}, theme.Right, theme.Left, true, true, true},
		// Normalized moves the panel off the sidebar's side.
		{"SameSide", func(l *theme.Layout) {
			l.SidebarSide, l.PanelSide = theme.Right, theme.Right
		}, theme.Right, theme.Left, false, true, true},
		{"Compact", func(l *theme.Layout) { l.Density = theme.Compact }, theme.Left, theme.Right, false, true, true},
	} {
		for _, bar := range []struct {
			name string
			bar  ui.TitleBar
		}{{"mac", ui.TitleBar{Height: 52, Left: 78}}, {"linux", ui.TitleBar{Height: 52, Right: 138}}} {
			t.Run(tc.name+"/"+bar.name, func(t *testing.T) {
				a := arranged(tc.change)
				tt := ui.NewTester(a.view, 1040, 680)
				tt.SetTitleBar(bar.bar)
				frames(tt, 2)
				back, bell := find(t, tt, "Back"), find(t, tt, "Notifications")
				if bell.X+bell.W-back.X <= 0 || !tt.HasText("Listen now") {
					t.Fatalf("main panel from %v to %v", back, bell)
				}
				switch nav, ok := tt.Find("Explore"); {
				case tc.sidebar == theme.Hidden && ok:
					t.Error("hidden sidebar shown")
				case tc.sidebar == theme.Left && (!ok || nav.X > back.X):
					t.Errorf("sidebar at %v, main panel at %v, want it left", nav, back)
				case tc.sidebar == theme.Right && (!ok || nav.X < bell.X):
					t.Errorf("sidebar at %v, main panel at %v, want it right", nav, bell)
				}
				switch hide, ok := tt.Find("Hide panel"); {
				case tc.panel == theme.Hidden && ok:
					t.Error("hidden panel shown")
				case tc.panel == theme.Left && (!ok || hide.X > back.X):
					t.Errorf("panel at %v, main panel at %v, want it left", hide, back)
				case tc.panel == theme.Right && (!ok || hide.X < bell.X):
					t.Errorf("panel at %v, main panel at %v, want it right", hide, bell)
				}
				if _, ok := tt.Find("Lyrics"); ok != tc.hasLyrics {
					t.Errorf("Lyrics button shown %v, want %v", ok, tc.hasLyrics)
				}
				if _, ok := tt.Find("Now playing"); ok != tc.hasCover {
					t.Errorf("cover button shown %v, want %v", ok, tc.hasCover)
				}
				shuffle := find(t, tt, "Shuffle")
				if isOnTop := shuffle.Y < back.Y; isOnTop != tc.isBarOnTop {
					t.Errorf("player bar at %v, top bar at %v, want on top %v", shuffle, back, tc.isBarOnTop)
				}
				// Whatever reaches the window's top corners keeps clear of
				// the window controls.
				w := float32(1040)
				for _, s := range []string{"Back", "Notifications", "Hide panel", "YT Music", "Midnight Arcade"} {
					r, ok := tt.Find(s)
					if !ok || r.Y > bar.bar.Height {
						continue
					}
					if r.X < bar.bar.Left || r.X+r.W > w-bar.bar.Right {
						t.Errorf("%q at %v under the window controls %+v", s, r, bar.bar)
					}
				}
			})
		}
	}
}

// The panel slides in from its window edge: part way in, it shows the
// side next to the main panel. A Tester's frame runs the whole slide, so the
// slot is drawn alone, part way.
func TestLayoutPanelSlidesFromItsEdge(t *testing.T) {
	const width, shown = 1040, 0.4
	for _, side := range []theme.Side{theme.Left, theme.Right} {
		a := arranged(func(l *theme.Layout) { l.SidebarSide, l.PanelSide = theme.Left+theme.Right-side, side })
		tt := ui.NewTester(func(c *ui.Context) {
			a.applyTheme(c)
			edge := ui.Row(c).Fill().AlignItems(ui.Stretch)
			if side == theme.Right {
				edge.Justify(ui.End)
			}
			edge.Children(func() { a.panelSlot(c, shown) })
		}, width, 680)
		l := &a.theme.Layout
		slot := (l.Panel + l.Gutter) * shown
		// The close button ends the panel's header; the tabs start it.
		switch side {
		case theme.Left:
			r, end := find(t, tt, "Hide panel"), slot-a.theme.Space(3.5)
			if r.W == 0 || math.Abs(float64(r.X+r.W-end)) > 0.01 {
				t.Errorf("left: close button at %v, want ending at %v", r, end)
			}
		case theme.Right:
			r, start := find(t, tt, "Panel"), width-slot+a.theme.Space(4)
			if r.W == 0 || math.Abs(float64(r.X-start)) > 0.01 {
				t.Errorf("right: tabs at %v, want from %v", r, start)
			}
		}

		// In the window, it closes and reopens on its side.
		tt = newTester(a)
		open := find(t, tt, "Hide panel")
		click(t, tt, "Hide panel")
		settle(t, tt, "panel closing", func() bool { _, ok := tt.Find("Hide panel"); return !ok })
		click(t, tt, "Queue")
		settle(t, tt, "panel opening", func() bool { r, ok := tt.Find("Hide panel"); return ok && r == open })
	}
}

// Without a panel the toggles open nothing, and the main panel takes its
// room.
func TestLayoutNoPanel(t *testing.T) {
	bellAt := func(a *app) float32 {
		tt := newTester(a)
		a.panel.isShown, a.panel.tab = true, tabLyrics
		frames(tt, 2)
		isPanelShown := tt.HasText("Lyrics placeholder") || tt.HasText("From Neon Tide")
		if a.theme.Layout.PanelSide == theme.Hidden && isPanelShown {
			t.Error("a hidden panel opened")
		}
		return find(t, tt, "Notifications").X
	}
	with := bellAt(idleApp())
	without := bellAt(arranged(func(l *theme.Layout) { l.PanelSide = theme.Hidden }))
	if d := theme.Default().Layout; without-with != d.Panel+d.Gutter {
		t.Errorf("top bar's end moved by %v without the panel, want %v", without-with, d.Panel+d.Gutter)
	}
}

// Without a sidebar, its pages stay reachable by shortcut.
func TestLayoutNoSidebarNavigates(t *testing.T) {
	a := arranged(func(l *theme.Layout) { l.SidebarSide = theme.Hidden })
	tt := newTester(a)
	for _, tc := range []struct {
		key  ui.Key
		path string
	}{{ui.Key2, pathExplore}, {ui.Key3, pathLibrary}, {ui.Key1, pathHome}} {
		tt.Key(ui.Cmd, tc.key)
		frames(tt, 1)
		wantPath(t, a, tc.path)
	}
}

func TestLayoutCompact(t *testing.T) {
	apart := func(d theme.Density) float32 {
		a, q := testApp()
		custom := theme.Default()
		custom.Layout.Density = d
		a.setTheme(custom)
		a.panel.isShown = false
		al := a.cat.Albums[0]
		a.router.Push(albumPath(al.ID))
		tt := newTester(a)
		settleLoads(t, tt, a, q)
		ys := rowsAt(t, tt, a.cat.AlbumTracks(al.ID)[:2])
		return ys[1] - ys[0]
	}
	comfy, compact := apart(theme.Comfortable), apart(theme.Compact)
	sizes := theme.Default().Layout.Sizes
	if comfy != sizes.Row || compact >= comfy {
		t.Errorf("rows %v apart, %v when compact", comfy, compact)
	}
}
