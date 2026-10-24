package quiet

import (
	"testing"
	"yt-music-player/internal/budget"
)

func TestBudgetNote(t *testing.T) {
	g := started(t)
	// 0 allocs today: Reset reuses the timer.
	budget.Allocs(t, 0, 1000, g.Note)
}

func TestBudgetDropped(t *testing.T) {
	g := started(t)
	// 0 allocs today: a flag and Note's reset.
	budget.Allocs(t, 0, 1000, g.Dropped)
}
