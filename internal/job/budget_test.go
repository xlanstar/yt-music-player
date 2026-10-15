package job

import (
	"testing"
	"yt-music-player/internal/budget"
)

func TestBudgetRun(t *testing.T) {
	var j Job
	queue := make(posts, 1)
	// 5 allocs today: the context, its cancel, the goroutine and the post.
	budget.Allocs(t, 6, 100, func() { roundTrip(&j, queue) })
}

func TestBudgetIdle(t *testing.T) {
	var j Job
	// 0 allocs today.
	budget.Allocs(t, 0, 100, func() { sinkBool = j.IsRunning() })
	budget.Allocs(t, 0, 100, j.Stop)
}
