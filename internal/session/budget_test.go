package session

import (
	"runtime"
	"testing"
	"weak"
	"yt-music-player/internal/budget"
	"yt-music-player/internal/catalog"
)

// TestBudgetIdle: every build asks these, as do each row's Like and Liked
// music's count, so they must not allocate.
func TestBudgetIdle(t *testing.T) {
	s := idleSession(t)
	cases := append(idleCases(s), idleCase{"LikedCount", func() { sinkInt = s.LikedCount() }})
	for _, tc := range cases {
		// 0 allocs today for each.
		t.Run(tc.name, func(t *testing.T) { budget.Allocs(t, 0, 100, tc.call) })
	}
}

func TestBudgetLikedSongs(t *testing.T) {
	s := idleSession(t)
	// 1 alloc today: the list.
	budget.Allocs(t, 1, 20, func() { sinkSongs = s.LikedSongs() })
}

func TestBudgetLikes(t *testing.T) {
	s := idleSession(t)
	on := false
	// 0 allocs today.
	budget.Allocs(t, 0, 100, func() {
		s.applyLike("liked-1234", on)
		on = !on
	})
	ids := s.likeOrder
	// 10 allocs today: the map of bigLikes likes.
	budget.Allocs(t, 12, 20, func() { s.seedLikes(ids) })
}

func TestBudgetSetLiked(t *testing.T) {
	s := idleSession(t)
	id := catalog.Placeholder().Songs[0].ID
	on := !s.IsLiked(id)
	// 1.5 allocs today: the answer's func, and for a like (every other
	// call) a copy of the song it describes, which the daemon tells the
	// other front-ends. Down from 5 since the daemon, not package account
	// in this process, sends the like.
	budget.Allocs(t, 1.5, 100, func() {
		s.SetLiked(id, on)
		on = !on
	})
}

func TestBudgetRetry(t *testing.T) {
	s, q := newSession(catalog.Placeholder())
	s.KeepAccount(catalog.Account{Name: "Lan"}, catalog.Library{})
	// 15 allocs today: the job's context, goroutine and post, the source's
	// copies, the maps of ShareStrings, Unique and the likes. 3 when ytmd
	// fetched them and sent the event.
	budget.Allocs(t, 18, 100, func() { retryLikes(s, q) })
}

// TestBudgetLiveLikes holds the heap a front-end keeps for 5000 liked songs
// described as YouTube does: the Session and its catalog, less once they
// are collected.
func TestBudgetLiveLikes(t *testing.T) {
	if budget.Race {
		t.Skip("the race detector allocates on its own")
	}
	s, q := sessionOver(decodedLikes{catalog.NewPlaceholderSource(0), 5000}, catalog.New(nil, nil, nil))
	s.KeepAccount(catalog.Account{Name: "Lan"}, catalog.Library{})
	retryLikes(s, q)
	if s.LikedCount() != 5000 {
		t.Fatalf("%d liked, want 5000", s.LikedCount())
	}
	with := liveHeap()
	gone := weak.Make(s)
	s = nil
	for range 10 {
		if gone.Value() == nil {
			break
		}
		runtime.GC()
	}
	if gone.Value() != nil {
		t.Fatal("the dropped Session stays reachable")
	}
	live := (with - liveHeap()) / 1024
	t.Logf("%d KB live", live)
	// 1845 to 1850 KB today; 2317 without catalog.ShareStrings.
	if live > 2130 {
		t.Errorf("%d KB live, budget 2130", live)
	}
}

func liveHeap() int64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int64(m.HeapAlloc)
}
