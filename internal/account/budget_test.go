package account

import (
	"strings"
	"testing"
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

func TestBudgetCleanCredentials(t *testing.T) {
	// 1 alloc today: the header collapsed onto one line.
	budget.Allocs(t, 1, 100, func() { sinkString = CleanCredentials(cookieHeader) })
	// 0 allocs today: on one line already, the header is its own part.
	oneLine := strings.Join(strings.Fields(cookieHeader), " ")
	budget.Allocs(t, 0, 100, func() { sinkString = CleanCredentials(oneLine) })
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
