package session

import (
	"testing"
	"yt-music-player/internal/budget"
	"yt-music-player/internal/catalog"
)

// TestBudgetIdle: every build asks these, as do each row's Like and Liked
// music's count, so they must not allocate.
func TestBudgetIdle(t *testing.T) {
	s := idleSession(t, bigLikes)
	cases := append(idleCases(s), idleCase{"LikedCount", func() { sinkInt = s.LikedCount() }})
	for _, tc := range cases {
		// 0 allocs today for each.
		t.Run(tc.name, func(t *testing.T) { budget.Allocs(t, 0, 100, tc.call) })
	}
}

func TestBudgetLikedSongs(t *testing.T) {
	s := idleSession(t, bigLikes)
	// 1 alloc today: the list.
	budget.Allocs(t, 1, 20, func() { sinkSongs = s.LikedSongs() })
}

func TestBudgetLikes(t *testing.T) {
	s := idleSession(t, bigLikes)
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
	s := idleSession(t, bigLikes)
	id := catalog.Placeholder().Songs[0].ID
	on := !s.Liked(id)
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
	s := idleSession(t, 0)
	likes := placeholderLikes(t)
	// 3 allocs today: the answer's func and the map of likes. Down from
	// 11 since the daemon, not package account in this process, fetches.
	budget.Allocs(t, 4, 100, func() { retryLikes(s, likes) })
}
