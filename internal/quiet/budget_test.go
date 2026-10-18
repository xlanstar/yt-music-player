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
