package settings

import (
	"testing"
	"yt-music-player/internal/budget"
	"yt-music-player/internal/theme"
)

func TestBudgetLoad(t *testing.T) {
	s := saved(t)
	// 7 allocs today.
	budget.Allocs(t, 9, 50, func() { sinkSettings, sinkErr = s.Load() })
}

func TestBudgetSave(t *testing.T) {
	s := saved(t)
	v := Settings{Theme: "midnight", Layout: theme.Arrangement{PlayerBar: "top"}}
	// 16 allocs today.
	budget.Allocs(t, 19, 10, func() { sinkErr = s.Save(v) })
}

func TestBudgetThemes(t *testing.T) {
	s := withThemes(t, 4)
	// 303 allocs today: 5 files parsed, one of every token.
	budget.Allocs(t, 349, 10, func() { sinkThemes, sinkErr = s.Themes() })
}

func TestBudgetResolve(t *testing.T) {
	users := []UserTheme{{File: "dusk.json", Theme: theme.Default()}}
	v := Settings{Theme: "dusk.json", Layout: theme.Arrangement{Sidebar: "right"}}
	// 0 allocs today.
	budget.Allocs(t, 0, 100, func() { sinkTheme, sinkErr, _ = Resolve(v, users) })
}
