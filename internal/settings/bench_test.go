package settings

import (
	"path/filepath"
	"strconv"
	"testing"
	"yt-music-player/internal/theme"
)

var (
	sinkSettings Settings
	sinkThemes   []UserTheme
	sinkTheme    theme.Theme
	sinkErr      error
)

// Settings load once a run and save once per choice, never per frame.

func saved(tb testing.TB) Dir {
	tb.Helper()
	s := store(tb)
	prefs := Settings{Theme: "dusk.json", Layout: theme.Arrangement{Sidebar: "right", Density: "compact"}}
	if err := s.Save(prefs); err != nil {
		tb.Fatal(err)
	}
	return s
}

// withThemes holds count theme files of the size users write, and one
// more with every token.
func withThemes(tb testing.TB, count int) Dir {
	tb.Helper()
	s := store(tb)
	for i := range count {
		write(tb, filepath.Join(s.ThemesDir(), "theme-"+strconv.Itoa(i)+".json"),
			`{"name": "Theme `+strconv.Itoa(i)+`", "seed": {"accent": "#7AA2F7", "base": "oklch(0.17 0.03 275)"}, "radius": {"card": 8}}`)
	}
	full, err := theme.Encode("Full", theme.Default())
	if err != nil {
		tb.Fatal(err)
	}
	write(tb, filepath.Join(s.ThemesDir(), "full.json"), string(full))
	return s
}

func BenchmarkLoad(b *testing.B) {
	s := saved(b)
	b.ReportAllocs()
	for b.Loop() {
		sinkSettings, sinkErr = s.Load()
	}
}

func BenchmarkSave(b *testing.B) {
	s := saved(b)
	v := Settings{Theme: "midnight", Layout: theme.Arrangement{PlayerBar: "top"}}
	b.ReportAllocs()
	for b.Loop() {
		sinkErr = s.Save(v)
	}
}

func BenchmarkThemes(b *testing.B) {
	for _, count := range []int{4, maxThemes - 1} {
		b.Run(strconv.Itoa(count+1), func(b *testing.B) {
			s := withThemes(b, count)
			b.ReportAllocs()
			for b.Loop() {
				sinkThemes, sinkErr = s.Themes()
			}
		})
	}
}

func BenchmarkResolve(b *testing.B) {
	users := []UserTheme{{File: "dusk.json", Theme: theme.Default()}}
	v := Settings{Theme: "dusk.json", Layout: theme.Arrangement{Sidebar: "right"}}
	b.ReportAllocs()
	for b.Loop() {
		sinkTheme, sinkErr, _ = Resolve(v, users)
	}
}
