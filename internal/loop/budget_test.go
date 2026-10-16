package loop

import (
	"testing"
	"yt-music-player/internal/budget"
)

func TestBudgetPost(t *testing.T) {
	l := New(nil)
	go l.Run()
	defer l.Stop()
	ran := make(chan struct{})
	fn := func() { ran <- struct{}{} }
	// 0 allocs today: the queue and the batch swap their arrays.
	budget.Allocs(t, 0, 1000, func() { postWait(l, ran, fn) })
}
