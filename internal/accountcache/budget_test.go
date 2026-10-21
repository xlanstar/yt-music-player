package accountcache

import (
	"testing"
	"yt-music-player/internal/budget"
)

// TestBudgetLoad holds a start's load of 5000 liked songs and a library of
// 500, and the file's size.
func TestBudgetLoad(t *testing.T) {
	f, size := savedFile(t)
	t.Logf("%d bytes", size)
	if size > 1.9e6 {
		t.Errorf("%d bytes, budget 1.9 MB", size)
	}
	// 34767 allocs, 4.02 MB today, mostly the decoded strings; reading the
	// file whole first, 5.68 MB.
	budget.Within(t, budget.Cost{Allocs: 40e3, Bytes: 4.62e6}, 5, func() { sinkSaved, _ = f.Load() })
}

func TestBudgetSave(t *testing.T) {
	f, _ := savedFile(t)
	s := bigSaved()
	// 14 allocs today, about 1.4 KB: the temporary file's paths, as long as
	// TMPDIR. The bytes' bound only holds the file streamed, not marshaled
	// whole (1.5 MB).
	budget.Within(t, budget.Cost{Allocs: 16, Bytes: 64e3}, 5, func() { _ = f.Save(s) })
}
