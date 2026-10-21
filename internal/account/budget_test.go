package account

import (
	"testing"
	"yt-music-player/internal/budget"
	"yt-music-player/internal/catalog"
)

func TestBudgetFetch(t *testing.T) {
	a, q := placeholderAccount(placeholder())
	// 7 allocs today, mostly the source's.
	budget.Allocs(t, 9, 100, func() { fetchRoundTrip(a, q) })
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

func TestBudgetStatus(t *testing.T) {
	a, q := placeholderAccount(placeholder())
	fetchRoundTrip(a, q)
	// 0 allocs today.
	budget.Allocs(t, 0, 100, func() { sinkStatus, sinkBool = a.Status() })
}
