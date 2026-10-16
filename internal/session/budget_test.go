package session

import (
	"testing"
	"yt-music-player/internal/budget"
	"yt-music-player/internal/catalog"
)

// TestBudgetIdle: every build asks these, as do each row's Like and Liked
// music's count, so they must not allocate.
func TestBudgetIdle(t *testing.T) {
	s, _ := idleSession(t, bigLikes)
	cases := append(idleCases(s), idleCase{"LikedCount", func() { sinkInt = s.LikedCount() }})
	for _, tc := range cases {
		// 0 allocs today for each.
		t.Run(tc.name, func(t *testing.T) { budget.Allocs(t, 0, 100, tc.call) })
	}
}

func TestBudgetLikedSongs(t *testing.T) {
	s, _ := idleSession(t, bigLikes)
	// 1 alloc today: the list.
	budget.Allocs(t, 1, 20, func() { sinkSongs = s.LikedSongs() })
}

func TestBudgetLikes(t *testing.T) {
	s, _ := idleSession(t, bigLikes)
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
	s, q := idleSession(t, bigLikes)
	id := catalog.Placeholder().Songs[0].ID
	on := !s.Liked(id)
	// 5 allocs today: the answer's func, the account's goroutine func and
	// post, and the change it reports (Likes and its slice). Raised from 2
	// when sending likes moved to package account, which reports each that
	// went through.
	budget.Allocs(t, 6, 100, func() {
		likeRoundTrip(s, q, id, on)
		on = !on
	})
}

func TestBudgetRetry(t *testing.T) {
	s, q := idleSession(t, 0)
	// 11 allocs today.
	budget.Allocs(t, 13, 100, func() {
		s.Retry(LikedRes)
		(<-q)()
	})
}
