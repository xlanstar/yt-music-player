package logfile

import (
	"testing"
	"yt-music-player/internal/budget"
)

func TestBudgetWrite(t *testing.T) {
	w := New(t.TempDir(), "ytmd")
	t.Cleanup(func() { _ = w.Close() })
	// 0 allocs today: the file is open, and each line goes straight to it.
	budget.Allocs(t, 0, 200, func() { sinkInt, sinkErr = w.Write(event) })
}

func TestBudgetLevel(t *testing.T) {
	// 0 allocs today: a known name, lowered in place when it is lower
	// case already.
	budget.Allocs(t, 0, 100, func() { _, sinkErr = Level("debug") })
}
