package player

import (
	"testing"
	"yt-music-player/internal/budget"
)

func TestBudgetNew(t *testing.T) {
	ids := queueIDs(bigQueue)
	// 3 allocs today: the player, the queue and its order.
	budget.Allocs(t, 4, 20, func() { sinkPlayer = New(ids) })
}

// TestBudgetMoves: these run on clicks, song ends and every build.
func TestBudgetMoves(t *testing.T) {
	for _, r := range repeats {
		t.Run(r.name, func(t *testing.T) {
			p := bigPlayer()
			p.Repeat = r.repeat
			// 0 allocs today, for each.
			budget.Allocs(t, 0, 100, func() { sinkString = p.Current() })
			budget.Allocs(t, 0, 100, p.Next)
			budget.Allocs(t, 0, 100, p.Prev)
			budget.Allocs(t, 0, 100, p.Finished)
		})
	}
}

func TestBudgetToggleShuffle(t *testing.T) {
	p := bigPlayer()
	// 1 alloc today: the new order.
	budget.Allocs(t, 1, 20, func() {
		p.isShuffled = false
		p.ToggleShuffle()
	})
	// 1 alloc today: the order.
	budget.Allocs(t, 1, 20, func() {
		p.isShuffled = true
		p.ToggleShuffle()
	})
}

func TestBudgetPlay(t *testing.T) {
	p := bigPlayer()
	ids := [2]string{"song-10", "song-1990"}
	i := 0
	// 0 allocs today.
	budget.Allocs(t, 0, 100, func() {
		p.Play(ids[i%2])
		i++
	})
	fresh := queueIDs(101)
	for k := range fresh {
		fresh[k] = "new-" + fresh[k]
	}
	i = 0
	// 0.01 allocs today: the queue and its order grow now and then, the
	// queue once in these 100 calls.
	budget.Allocs(t, 0.01, 100, func() {
		p.Play(fresh[i])
		i++
	})
}

func TestBudgetPlayAll(t *testing.T) {
	p := bigPlayer()
	ids := queueIDs(bigQueue)
	// 2 allocs today: the queue and its order.
	budget.Allocs(t, 2, 20, func() { p.PlayAll(ids) })
	p.isShuffled = true
	// 3 allocs today: plus the shuffled order.
	budget.Allocs(t, 3, 20, func() { p.PlayAll(ids) })
}

func TestBudgetAppendUpNext(t *testing.T) {
	p := bigPlayer()
	dst := make([]string, 0, 20)
	// 0 allocs today: the panel's slice is kept.
	budget.Allocs(t, 0, 100, func() { dst = p.AppendUpNext(dst[:0], 20) })
}

func TestBudgetAppendPlayOrder(t *testing.T) {
	p := bigPlayer()
	dst := make([]string, 0, bigQueue)
	// 0 allocs today: the daemon's slice is kept.
	budget.Allocs(t, 0, 100, func() { dst = p.AppendPlayOrder(dst[:0]) })
}

// TestBudgetQueueEdits: a front-end's edits of a long queue.
func TestBudgetQueueEdits(t *testing.T) {
	p := bigPlayer()
	// 0 allocs today: in place.
	budget.Allocs(t, 0, 100, func() {
		p.Move(10, bigQueue-10)
		p.Move(bigQueue-10, 10)
	})
	ids := []string{"new"}
	// 0 allocs today: the queue and order keep the room Remove leaves.
	budget.Allocs(t, 0, 100, func() {
		p.Enqueue(ids, true)
		p.Remove(p.at + 1)
	})
}
