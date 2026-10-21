package daemon

import (
	"encoding/gob"
	"io"
	"net"
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
	// 15 allocs today: the event and its State, the queue's IDs and songs,
	// the account's Latest, the frames, and the 8 events of the songs, which
	// go ahead of it (carry).
	budget.Allocs(t, 18, 20, func() { sinkFrames = s.snapshot() })
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

// TestBudgetLiveConn holds the heap a subscriber's connection keeps once
// it got the snapshot of 5000 liked songs and 5000 queued: with the
// connection, less once it closed.
func TestBudgetLiveConn(t *testing.T) {
	if budget.Race {
		t.Skip("the race detector allocates on its own")
	}
	h := newHarness(t, Options{Seed: catalogtest.Decoded(5000)})
	c := h.connect(true)
	h.startLiking(catalogtest.Decoded(5000))
	// All three told, so that nothing is in flight while measuring.
	for told := 0; told < 3; {
		if ev := c.event(); ev.Account != nil || ev.Library != nil || ev.Likes != nil && ev.Likes.Full {
			told++
		}
	}

	ours, theirs := net.Pipe()
	served := make(chan struct{})
	go func() {
		h.svc.Serve(theirs)
		close(served)
	}()
	readSnapshot(t, ours)
	go io.Copy(io.Discard, ours) //nolint:errcheck // ends as the pipe closes
	with := liveHeap()
	_ = ours.Close()
	<-served
	h.do(func() {}) // its unregister
	live := (with - liveHeap()) / 1024
	t.Logf("%d KB live", live)
	// 193 KB today, mostly gob's buffer for the snapshot's event, the songs
	// gone ahead (carry): with them, 2754.
	if live > 222 {
		t.Errorf("%d KB live, budget 222", live)
	}
}

// readSnapshot greets the daemon over nc as a subscriber and reads frames up
// to the reply of a ping, which follows the snapshot; its decoder goes
// with it, so that only the daemon's side stays.
func readSnapshot(t *testing.T, nc net.Conn) {
	t.Helper()
	enc, dec := gob.NewEncoder(nc), gob.NewDecoder(nc)
	hello := api.Hello{Proto: api.Proto, Version: "1.2.3", Role: api.RoleDesktop, Subscribe: true, PID: 78}
	for _, f := range []api.Frame{{Hello: &hello}, {ID: 1, Call: &api.Call{Method: api.MethodPing}}} {
		if err := enc.Encode(&f); err != nil {
			t.Fatal(err)
		}
	}
	for {
		var f api.Frame
		if err := dec.Decode(&f); err != nil {
			t.Fatal(err)
		}
		if f.Reply != nil && f.ID == 1 {
			return
		}
	}
}

func liveHeap() int64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int64(m.HeapAlloc)
}
