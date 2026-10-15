package settings

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"yt-music-player/internal/theme"
)

func store(tb testing.TB) Dir { return Dir(filepath.Join(tb.TempDir(), "yt-music-player")) }

func write(tb testing.TB, path, data string) {
	tb.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		tb.Fatal(err)
	}
}

func TestLoadMissing(t *testing.T) {
	v, err := store(t).Load()
	if err != nil || v != (Settings{}) {
		t.Errorf("Load = %+v, %v; want the defaults", v, err)
	}
	if v, err := Dir("").Load(); err != nil || v != (Settings{}) {
		t.Errorf("no directory: %+v, %v", v, err)
	}
}

func TestLoadCorrupt(t *testing.T) {
	for name, data := range map[string]string{
		"truncated":     `{"theme": "for`,
		"not an object": `[1]`,
		"wrong type":    `{"theme": 3}`,
	} {
		s := store(t)
		write(t, filepath.Join(string(s), fileName), data)
		v, err := s.Load()
		if err == nil || !strings.Contains(err.Error(), fileName) || v != (Settings{}) {
			t.Errorf("%s: Load = %+v, %v", name, v, err)
		}
	}
}

func TestSaveLoad(t *testing.T) {
	s := store(t)
	want := Settings{Theme: "midnight", Layout: theme.Arrangement{Sidebar: "right", Density: "compact"}}
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil || got != want {
		t.Errorf("Load = %+v, %v; want %+v", got, err, want)
	}
	// Saving again replaces it whole and leaves no temporary file.
	if err := s.Save(Settings{Theme: "forest"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Load(); got != (Settings{Theme: "forest"}) {
		t.Errorf("Load = %+v", got)
	}
	entries, err := os.ReadDir(string(s))
	if err != nil || len(entries) != 1 || entries[0].Name() != fileName {
		t.Errorf("directory holds %v, %v", entries, err)
	}
	// Unknown fields come from a later version, not mistakes.
	write(t, filepath.Join(string(s), fileName), `{"theme": "orchid", "volume": 3}`)
	if got, err := s.Load(); err != nil || got.Theme != "orchid" {
		t.Errorf("Load = %+v, %v", got, err)
	}
}

func TestSavePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix permissions")
	}
	s := store(t)
	if err := s.Save(Settings{Theme: "studio"}); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{string(s): 0o700, filepath.Join(string(s), fileName): 0o600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s: %v, want %v", filepath.Base(path), got, want)
		}
	}
}

func TestSaveFailsWhole(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix permissions")
	}
	s := store(t)
	if err := s.Save(Settings{Theme: "forest"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(string(s), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(string(s), 0o700) })
	if err := s.Save(Settings{Theme: "orchid"}); err == nil {
		t.Skip("the directory stayed writable, as for root")
	}
	if got, err := s.Load(); err != nil || got.Theme != "forest" {
		t.Errorf("Load = %+v, %v", got, err)
	}
}

func TestThemes(t *testing.T) {
	s := store(t)
	if themes, err := s.Themes(); err != nil || themes != nil {
		t.Errorf("no directory: %v, %v", themes, err)
	}
	dir := s.ThemesDir()
	write(t, filepath.Join(dir, "dusk.json"), `{"name": "Dusk", "seed": {"accent": "#7AA2F7"}}`)
	write(t, filepath.Join(dir, "plain.JSON"), `{"radius": {"card": 4}}`)
	write(t, filepath.Join(dir, "broken.json"), `{"colour": {}}`)
	write(t, filepath.Join(dir, "notes.txt"), `not a theme`)
	write(t, filepath.Join(dir, ".hidden.json"), `{}`)
	write(t, filepath.Join(dir, "huge.json"), `{"name": "`+strings.Repeat("a", theme.MaxFileSize)+`"}`)
	if err := os.Mkdir(filepath.Join(dir, "folder.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	themes, err := s.Themes()
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, user := range themes {
		files = append(files, user.File)
	}
	if strings.Join(files, " ") != "broken.json dusk.json huge.json plain.JSON" {
		t.Fatalf("themes %q", files)
	}
	broken, dusk, huge, plain := themes[0], themes[1], themes[2], themes[3]
	isUnprefixed := broken.Err != nil && !strings.HasPrefix(broken.Err.Error(), "theme:")
	if !isUnprefixed || !strings.Contains(broken.Err.Error(), `unknown object member name "colour"`) {
		t.Errorf("broken: %v", broken.Err)
	}
	if huge.Err == nil || !strings.Contains(huge.Err.Error(), "larger than") {
		t.Errorf("huge: %v", huge.Err)
	}
	if dusk.Err != nil || dusk.Name != "Dusk" || dusk.Theme.Color.Accent == theme.Default().Color.Accent {
		t.Errorf("dusk: %q, %v", dusk.Name, dusk.Err)
	}
	if plain.Err != nil || plain.Name != "plain" || plain.Theme.Radius.Card != 4 {
		t.Errorf("plain: %q, %v", plain.Name, plain.Err)
	}
}

func TestResolve(t *testing.T) {
	dusk := theme.Derive(theme.Seed{Accent: theme.Oklch(0.7, 0.1, 250)})
	users := []UserTheme{
		{File: "dusk.json", Name: "Dusk", Theme: dusk},
		{File: "broken.json", Name: "broken", Err: errors.New("bad color")},
	}
	studio, _ := theme.PresetByID("studio")
	for _, tc := range []struct {
		name string
		v    Settings
		want theme.Theme
		err  string
	}{
		{"default", Settings{}, theme.Default(), ""},
		{"preset", Settings{Theme: "studio"}, studio.Theme(), ""},
		{"user", Settings{Theme: "dusk.json"}, dusk, ""},
		{"broken", Settings{Theme: "broken.json"}, theme.Default(), "bad color"},
		{"gone", Settings{Theme: "gone.JSON"}, theme.Default(), "no such theme file"},
		{"unknown preset", Settings{Theme: "neon"}, theme.Default(), "no such preset"},
		{"preset as file", Settings{Theme: "studio.json"}, theme.Default(), "no such theme file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err, layoutErr := Resolve(tc.v, users)
			isWantedErr := tc.err == "" && err == nil || tc.err != "" && err != nil && err.Error() == tc.err
			if layoutErr != nil || !isWantedErr {
				t.Errorf("errors %v, %v; want %q", err, layoutErr, tc.err)
			}
			if got != tc.want {
				t.Error("resolved another theme")
			}
		})
	}
	// The user's layout goes over the theme's, normalized.
	prefs := Settings{Theme: "studio", Layout: theme.Arrangement{Sidebar: "right", PlayerBar: "bottom"}}
	got, err, layoutErr := Resolve(prefs, users)
	if err != nil || layoutErr != nil {
		t.Fatal(err, layoutErr)
	}
	layout := got.Layout
	isArranged := layout.SidebarSide == theme.Right && layout.PanelSide == theme.Left && layout.PlayerBar == theme.Bottom
	if !isArranged || layout.Density != theme.Compact {
		t.Errorf("layout %+v", layout)
	}
	// An unreadable layout keeps the theme's.
	got, err, layoutErr = Resolve(Settings{Theme: "studio", Layout: theme.Arrangement{Sidebar: "up"}}, users)
	isLayoutErr := layoutErr != nil && strings.Contains(layoutErr.Error(), "layout: sidebar")
	if err != nil || !isLayoutErr || got.Layout != studio.Theme().Layout {
		t.Errorf("layout %+v, %v", got.Layout, layoutErr)
	}
}

func TestSaver(t *testing.T) {
	s := store(t)
	failed := make(chan error, 8)
	saver := NewSaver(s, func(err error) { failed <- err })
	for _, id := range []string{"forest", "orchid", "midnight", "studio"} {
		saver.Save(Settings{Theme: id})
	}
	saver.Wait()
	if got, err := s.Load(); err != nil || got.Theme != "studio" {
		t.Errorf("Load = %+v, %v", got, err)
	}
	if len(failed) != 0 {
		t.Error("told of a failure that was not")
	}
	// A file where the directory should be makes the write fail.
	saver = NewSaver(Dir(filepath.Join(string(s), fileName)), func(err error) { failed <- err })
	saver.Save(Settings{Theme: "forest"})
	saver.Wait()
	if len(failed) != 1 {
		t.Error("a failed write went untold")
	}
	// An empty Dir never fails, so failed may be nil.
	saver = NewSaver("", nil)
	saver.Save(Settings{Theme: "forest"})
	saver.Wait()
}
