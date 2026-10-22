package session

import (
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
	// 0 allocs today: the map of the likes is reused.
	budget.Allocs(t, 0, 20, func() { s.seedLikes(ids) })
	// One a call: budget.Measure's warm-up and 20 runs.
	next := fresh(21, newSessions)
	// 10 allocs today: bigLikes into a Session's first map, made to size.
	budget.Allocs(t, 12, 20, func() { next().seedLikes(ids) })
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
	for _, tc := range retryCases {
		t.Run(tc.name, func(t *testing.T) {
			s, q := retrySession(tc.session)
			budget.Allocs(t, tc.limit, 20, func() { retryLikes(s, q) })
		})
	}
}

func TestBudgetCacheRead(t *testing.T) {
	saved := lanCached()
	next := fresh(21, lanSessions)
	// 81 allocs and 2.5 MB today for 5000 liked, once a start, into an
	// empty catalog and the Session's first map of the likes.
	budget.Within(t, budget.Cost{Allocs: 94, Bytes: 2.87e6}, 20, func() { next().cacheRead(saved, nil) })
}

func TestBudgetSaveCache(t *testing.T) {
	s, q, saved := cachedSession()
	s.cacheRead(saved, nil)
	s.isLibraryFresh, s.isLikesFresh = true, true
	// 11 allocs today: the IDs and songs liked, copied for the writer, the
	// job's and the channel Wait reads.
	budget.Allocs(t, 13, 20, func() { saveRoundTrip(s, q) })
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
	with := budget.LiveHeap()
	gone := weak.Make(s)
	s = nil
	if !budget.IsCollected(gone) {
		t.Fatal("the dropped Session stays reachable")
	}
	live := (with - budget.LiveHeap()) / 1024
	t.Logf("%d KB live", live)
	// 1845 to 1850 KB today; 2317 without catalog.ShareStrings.
	if live > 2130 {
		t.Errorf("%d KB live, budget 2130", live)
	}
}
