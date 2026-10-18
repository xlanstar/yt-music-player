package daemon

import (
	"path/filepath"
	"testing"
	"yt-music-player/internal/budget"
)

func TestBudgetDispatch(t *testing.T) {
	for _, tc := range dispatchCases {
		t.Run(tc.name, func(t *testing.T) {
			s, c := benchService(t, tc.queue)
			i := 0
			budget.Allocs(t, tc.limit, 100, func() {
				tc.op(s, c, i)
				i++
			})
		})
	}
}

// TestBudgetSettle: the loop settles after every batch, mostly with
// nothing to tell.
func TestBudgetSettle(t *testing.T) {
	s, _ := benchService(t, bigQueue)
	// 0 allocs today.
	budget.Allocs(t, 0, 100, s.settle)
}

func TestBudgetLikesDiff(t *testing.T) {
	for _, tc := range likesCases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := benchService(t, 3)
			budget.Allocs(t, tc.limit, 100, func() { s.accountChanged(tc.change) })
		})
	}
}

func TestBudgetSnapshot(t *testing.T) {
	s, _ := benchService(t, bigQueue)
	// 6 allocs today: the event and its State, the queue's IDs and songs,
	// the account's Latest, the frames.
	budget.Allocs(t, 7, 20, func() { sinkFrames = s.snapshot() })
}

func TestBudgetWrite(t *testing.T) {
	s, _ := benchService(t, bigQueue)
	st := s.saved()
	w := newStore(filepath.Join(t.TempDir(), "daemon.json"), 0)
	// 15.1 allocs, 173 KB today: a 172 KB file, encoded to bytes first.
	budget.Within(t, budget.Cost{Allocs: 18, Bytes: 2e5}, 20, func() { w.write(st) })
}
