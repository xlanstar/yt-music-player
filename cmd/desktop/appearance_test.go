package main

import (
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"yt-music-player/internal/settings"
	"yt-music-player/internal/theme"

	"github.com/egoist/mygo/ui"
)

// lookApp is an idle app with its settings in a temporary directory,
// which prepare fills first.
func lookApp(t *testing.T, prepare func(dir settings.Dir)) (*app, posts, settings.Dir) {
	t.Helper()
	dir := settings.Dir(filepath.Join(t.TempDir(), "yt-music-player"))
	if prepare != nil {
		prepare(dir)
	}
	a, q := testApp()
	preload(a)
	a.useSettings(dir)
	t.Cleanup(a.look.saver.Wait)
	return a, q, dir
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// saved is what dir holds once the app's pending saves finish.
func saved(t *testing.T, a *app, dir settings.Dir) settings.Settings {
	t.Helper()
	a.look.saver.Wait()
	loaded, err := dir.Load()
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

// windowAt is the window's color by the sidebar's edge, where nothing
// covers the background.
func windowAt(tt *ui.Tester) color.RGBA { return tt.Image().RGBAAt(3, 500) }

func rgba(c ui.Color) color.RGBA { return color.RGBA{c.R, c.G, c.B, c.A} }

func openLookSheet(t *testing.T, tt *ui.Tester, q posts, a *app) {
	t.Helper()
	click(t, tt, "Appearance")
	frames(tt, 2)
	drain(q)
	frames(tt, 2)
	if !a.look.isOpen || !tt.HasText("LAYOUT") {
		t.Fatalf("no Appearance sheet, texts %q", tt.Texts())
	}
}

func TestAppearanceChoosesPreset(t *testing.T) {
	a, q, dir := lookApp(t, nil)
	tt := newTester(a)
	if got, want := windowAt(tt), rgba(theme.Default().Color.Base); got != want {
		t.Fatalf("window %v, want %v", got, want)
	}
	openLookSheet(t, tt, q, a)
	click(t, tt, "Midnight")
	frames(tt, 2)
	midnight, _ := theme.PresetByID("midnight")
	want := midnight.Theme()
	if a.theme != want || th != &a.theme || a.contrast != want.HighContrast() {
		t.Fatal("Midnight is not the theme in use")
	}
	tt.Key(0, ui.KeyEscape)
	frames(tt, 2)
	if got := windowAt(tt); got != rgba(want.Color.Base) {
		t.Errorf("window %v, want Midnight's %v", got, rgba(want.Color.Base))
	}
	if got := saved(t, a, dir); got != (settings.Settings{Theme: "midnight"}) {
		t.Errorf("saved %+v", got)
	}
	// The next run starts with it.
	b, _, _ := lookApp(t, nil)
	b.useSettings(dir)
	if b.theme != want {
		t.Error("the next run did not start with Midnight")
	}
}

func TestAppearanceArranges(t *testing.T) {
	a, q, dir := lookApp(t, nil)
	tt := newTester(a)
	openLookSheet(t, tt, q, a)
	sidebar, panel := find(t, tt, "Sidebar"), find(t, tt, "Now playing panel")
	// find returns the sidebar row's chip; the panel row's same-named
	// chip is offset by the rows' distance.
	chipIn := func(row ui.Rect, name string) {
		t.Helper()
		r := find(t, tt, name)
		tt.ClickAt(r.X+r.W/2, r.Y+r.H/2+row.Y-sidebar.Y)
		frames(tt, 2)
	}
	chipIn(sidebar, "Right")
	l := a.theme.Layout
	if l.SidebarSide != theme.Right || l.PanelSide != theme.Left {
		t.Fatalf("sidebar %v, panel %v", l.SidebarSide, l.PanelSide)
	}
	if got := saved(t, a, dir).Layout; got != (theme.Arrangement{Sidebar: "right"}) {
		t.Errorf("saved %+v", got)
	}
	// A panel moved to the sidebar's side takes it.
	chipIn(panel, "Right")
	if l = a.theme.Layout; l.SidebarSide != theme.Left || l.PanelSide != theme.Right {
		t.Fatalf("sidebar %v, panel %v", l.SidebarSide, l.PanelSide)
	}
	chipIn(sidebar, "Hidden")
	click(t, tt, "Top")
	click(t, tt, "Compact")
	frames(tt, 2)
	want := theme.Arrangement{Sidebar: "hidden", Panel: "right", PlayerBar: "top", Density: "compact"}
	if got := theme.ArrangementOf(a.theme.Layout); got != want {
		t.Errorf("arrangement %+v, want %+v", got, want)
	}
	if got := saved(t, a, dir).Layout; got != want {
		t.Errorf("saved %+v, want %+v", got, want)
	}
	click(t, tt, "Done")
	frames(tt, 2)
	if a.look.isOpen {
		t.Fatal("Done left the sheet open")
	}
	if _, ok := tt.Find("Explore"); ok {
		t.Error("the hidden sidebar shows")
	}
	if shuffle, back := find(t, tt, "Shuffle"), find(t, tt, "Back"); shuffle.Y > back.Y {
		t.Errorf("player bar at %v, top bar at %v, want it above", shuffle, back)
	}
	openLookSheet(t, tt, q, a)
	click(t, tt, "Use the theme’s")
	frames(tt, 2)
	if a.theme.Layout != theme.Default().Layout || saved(t, a, dir).Layout != (theme.Arrangement{}) {
		t.Errorf("layout %+v after reset", a.theme.Layout)
	}
}

// A user theme that cannot be read says why and cannot be chosen.
func TestAppearanceUserThemes(t *testing.T) {
	example, err := os.ReadFile("../../internal/theme/testdata/dusk.json")
	if err != nil {
		t.Fatal(err)
	}
	a, q, dir := lookApp(t, nil)
	tt := newTester(a)
	// Written after start: opening the sheet must reread them.
	writeFile(t, filepath.Join(dir.ThemesDir(), "dusk.json"), string(example))
	writeFile(t, filepath.Join(dir.ThemesDir(), "broken.json"), `{"color": {"accent": "teal"}}`)
	openLookSheet(t, tt, q, a)
	if !tt.HasText("Dusk") || !tt.HasText(`color.accent: invalid color "teal": want #RRGGBB or oklch(L C H)`) {
		t.Fatalf("texts %q", tt.Texts())
	}
	if !toasted(tt, "Couldn’t read the theme broken.json") {
		t.Errorf("no toast for the broken theme, announcements %q", tt.Announcements())
	}
	click(t, tt, "broken")
	frames(tt, 2)
	if a.look.set.Theme != "" || a.theme != theme.Default() {
		t.Error("chose a theme that cannot be read")
	}
	click(t, tt, "Dusk")
	frames(tt, 2)
	_, dusk, err := theme.Parse(example)
	if err != nil {
		t.Fatal(err)
	}
	if a.theme != dusk {
		t.Error("Dusk is not the theme in use")
	}
	if got := saved(t, a, dir); got.Theme != "dusk.json" {
		t.Errorf("saved %+v", got)
	}
}

func TestAppearanceBrokenThemeFallsBack(t *testing.T) {
	a, _, _ := lookApp(t, func(dir settings.Dir) {
		writeFile(t, filepath.Join(dir.ThemesDir(), "neon.json"), `{"type": {"title": 400}}`)
		if err := dir.Save(settings.Settings{Theme: "neon.json"}); err != nil {
			t.Fatal(err)
		}
	})
	tt := newTester(a)
	frames(tt, 2)
	if a.theme != theme.Default() {
		t.Error("the broken theme's fallback is not Default")
	}
	// Announcements() consumes them, so read once.
	if said := strings.Join(tt.Announcements(), "\n"); !strings.Contains(said, "Couldn’t read the theme neon.json") ||
		!strings.Contains(said, "type.title: 400 is out of 6 to 96") {
		t.Errorf("announcements %q", said)
	}
	if a.look.set.Theme != "neon.json" {
		t.Error("the choice was forgotten, which a fixed file would bring back")
	}
}

func TestAppearanceCorruptSettings(t *testing.T) {
	a, _, dir := lookApp(t, func(empty settings.Dir) {
		writeFile(t, filepath.Join(string(empty), "settings.json"), `{"theme": `)
	})
	tt := newTester(a)
	frames(tt, 2)
	if a.theme != theme.Default() || !toasted(tt, "Couldn’t read your settings") {
		t.Error("no toast for the settings")
	}
	// A choice then overwrites the corrupt file.
	a.chooseTheme("forest")
	if got := saved(t, a, dir); got.Theme != "forest" {
		t.Errorf("saved %+v", got)
	}
}

func TestAppearanceKeyboard(t *testing.T) {
	a, _, _ := lookApp(t, nil)
	tt := newTester(a)
	tt.Key(ui.Cmd, ui.KeyComma)
	frames(tt, 2)
	if !a.look.isOpen {
		t.Fatal("⌘, did not open the sheet")
	}
	// Tab reaches the themes; arrows choose among them.
	for range 8 {
		if tt.Focused("Default") {
			break
		}
		tt.Key(0, ui.KeyTab)
	}
	if !tt.Focused("Default") {
		t.Fatal("Tab does not reach the themes")
	}
	tt.Key(0, ui.KeyRight)
	frames(tt, 2)
	if a.look.set.Theme != "midnight" {
		t.Errorf("the arrows chose %q", a.look.set.Theme)
	}
	tt.Key(0, ui.KeyEscape)
	frames(tt, 2)
	if a.look.isOpen {
		t.Error("Escape left the sheet open")
	}
}

func TestAppearanceKeepsNothingWithoutDir(t *testing.T) {
	a := idleApp()
	a.chooseTheme("orchid")
	a.look.saver.Wait()
	if a.look.dir != "" || len(a.look.notes) != 0 {
		t.Errorf("an app without a store kept its settings in %q, or failed: %v", a.look.dir, a.look.notes)
	}
	orchid, _ := theme.PresetByID("orchid")
	if a.theme != orchid.Theme() {
		t.Error("Orchid is not the theme in use")
	}
}

// A steady frame costs no more allocations with any theme than with
// Default: frames read the chosen theme's tokens in place.
func TestThemeFrameAllocations(t *testing.T) {
	allocs := func(id string) float64 {
		s := newScreen(t, func(a *app) { a.chooseTheme(id) })
		s.steady()
		return testing.AllocsPerRun(10, s.tt.Frame)
	}
	want := allocs(theme.DefaultID)
	for _, p := range theme.Presets()[1:] {
		if got := allocs(p.ID); got > want {
			t.Errorf("%s: %v allocations a frame, Default %v", p.ID, got, want)
		}
	}
}
