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
