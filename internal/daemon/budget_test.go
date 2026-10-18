package daemon

import (
	"path/filepath"
	"runtime"
	"testing"
	"weak"
	"yt-music-player/internal/api"
	"yt-music-player/internal/budget"
	"yt-music-player/internal/catalog/catalogtest"
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
	// 14 allocs, 1.2 KB today: the temporary file's; encoding the 172 KB
	// to bytes first made 15.1 and 173 KB.
	budget.Within(t, budget.Cost{Allocs: 17, Bytes: 1.5e3}, 20, func() { w.write(st) })
}

// TestBudgetLiveQueue holds the heap a Service keeps for a queue of 5000
// songs as front-ends send them, each string a copy of its own, played
// twice, as the queue changes: with the Service, less once it is
// collected.
func TestBudgetLiveQueue(t *testing.T) {
	if budget.Race {
		t.Skip("the race detector allocates on its own")
	}
	s, c := benchService(t, 3)
	for range 2 {
		songs := catalogtest.Decoded(5000)
		ids := make([]string, len(songs))
		for i, song := range songs {
			ids[i] = song.ID
		}
		request(s, c, &api.Call{Method: api.MethodPlayAll, IDs: &api.IDsArgs{IDs: ids, Songs: songs}})
	}
	if s.player.Len() != 5000 {
		t.Fatalf("%d songs queued, want 5000", s.player.Len())
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
		t.Fatal("the dropped Service stays reachable")
	}
	live := (with - liveHeap()) / 1024
	t.Logf("%d KB live", live)
	// 2104 KB today, the songs' strings shared (catalog.ShareStrings);
	// unshared, 2575.
	if live > 2420 {
		t.Errorf("%d KB live, budget 2420", live)
	}
}

func liveHeap() int64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int64(m.HeapAlloc)
}
