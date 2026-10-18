package account

import (
	"runtime"
	"testing"
	"weak"
	"yt-music-player/internal/budget"
	"yt-music-player/internal/catalog"
)

func TestBudgetFetch(t *testing.T) {
	for _, tc := range fetchCases {
		t.Run(tc.name, func(t *testing.T) {
			a, q := placeholderAccount(tc.src())
			budget.Allocs(t, tc.limit, 100, func() { fetchRoundTrip(a, q, tc.part) })
		})
	}
}

func TestBudgetLike(t *testing.T) {
	a, q := placeholderAccount(placeholder())
	id := catalog.Placeholder().Songs[0].ID
	on := true
	// 4 allocs today: the goroutine's func, the post, and the change's
	// Likes and its slice.
	budget.Allocs(t, 5, 100, func() {
		likeRoundTrip(a, q, id, on)
		on = !on
	})
}

func TestBudgetLatest(t *testing.T) {
	a := bigLatest(t)
	// 2 allocs today: the Likes and a copy of their IDs.
	budget.Allocs(t, 2, 100, func() { sinkLatest = a.Latest() })
}

func TestBudgetKeepLike(t *testing.T) {
	a := bigLatest(t)
	added := Change{Likes: &Likes{Added: []string{"liked-1000"}}}
	removed := Change{Likes: &Likes{Removed: []string{"liked-1000"}}}
	// 0 allocs today: the IDs keep their room.
	budget.Allocs(t, 0, 100, func() {
		a.keep(added)
		a.keep(removed)
	})
}

// TestBudgetLiveLikes holds the heap an Account keeps for 5000 liked
// songs described as YouTube does: with the Account, less once it is
// collected.
func TestBudgetLiveLikes(t *testing.T) {
	if budget.Race {
		t.Skip("the race detector allocates on its own")
	}
	a, q := placeholderAccount(decoded5000())
	fetchRoundTrip(a, q, Liked)
	if len(a.likedSongs) != 5000 {
		t.Fatalf("%d liked songs, want 5000", len(a.likedSongs))
	}
	with := liveHeap()
	gone := weak.Make(a)
	a = nil
	for range 10 {
		if gone.Value() == nil {
			break
		}
		runtime.GC()
	}
	if gone.Value() != nil {
		t.Fatal("the dropped Account stays reachable")
	}
	live := (with - liveHeap()) / 1024
	t.Logf("%d KB live", live)
	// 1339 to 1385 KB today; without shareStrings, 1850.
	if live > 1593 {
		t.Errorf("%d KB live, budget 1593", live)
	}
}

func liveHeap() int64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int64(m.HeapAlloc)
}
