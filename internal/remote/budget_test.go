package remote

import (
	"runtime"
	"testing"
	"yt-music-player/internal/api"
	"yt-music-player/internal/budget"
	"yt-music-player/internal/catalog/catalogtest"
	"yt-music-player/internal/ipc"
)

// TestBudgetApplyEvents holds an event's way through the front-end's
// thread: queued by the reader, flushed into the Mirror. The reader's
// queue and the flushed batch swap, so neither grows again.
func TestBudgetApplyEvents(t *testing.T) {
	for _, tc := range applyCases {
		t.Run(tc.name, func(t *testing.T) {
			evs := tc.events()
			c, l := applying(len(evs))
			budget.Allocs(t, tc.allocs, 100, func() { applyEvents(c, l, evs, tc.relink) })
		})
	}
}

// TestBudgetLiveCall holds the heap a front-end's connection keeps once
// it enqueued 5000 songs: with the connection, less once it closed.
func TestBudgetLiveCall(t *testing.T) {
	if budget.Race {
		t.Skip("the race detector allocates on its own")
	}
	songs := catalogtest.Decoded(5000)
	ids := make([]string, len(songs))
	for i, s := range songs {
		ids[i] = s.ID
	}
	d, f := newDaemon("1.0.0"), newFront()
	conn := ipc.NewConn(d.connect(t.Context()))
	h, err := greet(t.Context(), conn, hello(api.RoleTUI, "1.0.0", true))
	if err != nil {
		t.Fatal(err)
	}
	f.client.open(conn, compare(h, "1.0.0"), false)
	f.client.Enqueue(ids, false, songs)
	// Its reply came, and the writer is done with it.
	if err := ping(t.Context(), f.client); err != nil {
		t.Fatal(err)
	}
	with := liveHeap()
	f.client.Close()
	d.exit()
	live := (with - liveHeap()) / 1024
	// The daemon keeps what it received in both.
	runtime.KeepAlive(d)
	t.Logf("%d KB live", live)
	// 155 to 160 KB today: gob's buffer for a part of 313 songs (72 KB),
	// both ends' buffers and decoders; the call whole, 1456.
	if live > 185 {
		t.Errorf("%d KB live, budget 185", live)
	}
}

func liveHeap() int64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int64(m.HeapAlloc)
}
