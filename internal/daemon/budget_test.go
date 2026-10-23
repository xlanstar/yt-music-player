package daemon

import (
	"encoding/gob"
	"encoding/json/v2"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"weak"
	"yt-music-player/internal/api"
	"yt-music-player/internal/budget"
	"yt-music-player/internal/catalog"
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
	// the frames, and the 8 events of the songs, which go ahead of it
	// (carry).
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
// songs as front-ends send them, each string a copy of its own, then
// another list played: with the Service, less once it is collected.
func TestBudgetLiveQueue(t *testing.T) {
	if budget.Race {
		t.Skip("the race detector allocates on its own")
	}
	for _, tc := range []struct {
		name string
		// then is the list played second: so many songs, other ones if
		// other.
		then  int
		other bool
		limit int
	}{
		// 1683 to 1687 KB today, the songs' strings shared
		// (catalog.ShareStrings), the queue's IDs theirs; unshared, 2153.
		{"replayed", 5000, false, 1940},
		// 1604 to 1609 KB today: the catalog's songs and index sized for the
		// second list, not both.
		{"replaced", 5000, true, 1860},
		// An album after a long list: 18 to 26 KB today, the long list's
		// arrays gone.
		{"shortened", 12, true, 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c := benchService(t, 3)
			playDecoded(s, c, 5000, false)
			playDecoded(s, c, tc.then, tc.other)
			if s.player.Len() != tc.then {
				t.Fatalf("%d songs queued, want %d", s.player.Len(), tc.then)
			}
			with := budget.LiveHeap()
			gone := weak.Make(s)
			s = nil
			if !budget.IsCollected(gone) {
				t.Fatal("the dropped Service stays reachable")
			}
			live := int((with - budget.LiveHeap()) / 1024)
			t.Logf("%d KB live", live)
			if live > tc.limit {
				t.Errorf("%d KB live, budget %d", live, tc.limit)
			}
		})
	}
}

// playDecoded has s play n songs shaped as YouTube's, as a front-end sends
// them, every string a copy of its own; other changes their IDs.
func playDecoded(s *Service, c *conn, n int, other bool) {
	songs := catalogtest.Decoded(n)
	ids := make([]string, n)
	for i := range songs {
		if other {
			songs[i].ID = "w" + songs[i].ID[1:]
		}
		ids[i] = strings.Clone(songs[i].ID)
	}
	request(s, c, &api.Call{Method: api.MethodPlayAll, IDs: &api.IDsArgs{IDs: ids, Songs: songs}})
}

// TestBudgetLiveRestored holds the heap a Service keeps for a queue of
// 5000 songs it read from daemon.json: with the Service, less once it is
// collected.
func TestBudgetLiveRestored(t *testing.T) {
	if budget.Race {
		t.Skip("the race detector allocates on its own")
	}
	path := filepath.Join(t.TempDir(), "daemon.json")
	writeQueue(t, path, catalogtest.Decoded(5000))
	s := newService(Options{Source: catalog.NewPlaceholderSource(0), State: path})
	if s.player.Len() != 5000 {
		t.Fatalf("%d songs queued, want 5000", s.player.Len())
	}
	with := budget.LiveHeap()
	gone := weak.Make(s)
	s = nil
	if !budget.IsCollected(gone) {
		t.Fatal("the dropped Service stays reachable")
	}
	live := (with - budget.LiveHeap()) / 1024
	t.Logf("%d KB live", live)
	// 1605 to 1616 KB today, the songs' strings shared and the queue's
	// IDs theirs; the IDs apart, 1694.
	if live > 1860 {
		t.Errorf("%d KB live, budget 1860", live)
	}
}

// writeQueue writes a daemon.json at path that queues songs.
func writeQueue(t *testing.T, path string, songs []catalog.Song) {
	t.Helper()
	data, err := json.Marshal(savedState{V: stateVersion, Queue: ids(songs), Volume: 0.5, Songs: savedSongs(songs)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestBudgetLiveConn holds the heap a subscriber's connection keeps once
// it got the snapshot of 5000 queued songs: with the connection, less once
// it closed.
func TestBudgetLiveConn(t *testing.T) {
	if budget.Race {
		t.Skip("the race detector allocates on its own")
	}
	h := newHarness(t, Options{Seed: catalogtest.Decoded(5000)})
	ours, leave := h.serveOne()
	readSnapshot(t, ours)
	go io.Copy(io.Discard, ours) //nolint:errcheck // ends as the pipe closes
	with := budget.LiveHeap()
	leave()
	live := (with - budget.LiveHeap()) / 1024
	t.Logf("%d KB live", live)
	// 127 KB today: 99 any idle connection's (TestBudgetLiveIdle), the
	// rest gob's buffer for an event of 313 songs gone ahead (carry: 5000
	// in 16).
	if live > 146 {
		t.Errorf("%d KB live, budget 146", live)
	}
}

// TestBudgetLiveReply holds the heap a connection keeps once it got a reply
// of 5000 liked songs: with the connection, less once it closed.
func TestBudgetLiveReply(t *testing.T) {
	if budget.Race {
		t.Skip("the race detector allocates on its own")
	}
	h := newHarness(t, Options{})
	ours, leave := h.serveOne()
	fetchLiked(t, h, ours, false)
	go io.Copy(io.Discard, ours) //nolint:errcheck // ends as the pipe closes
	with := budget.LiveHeap()
	leave()
	live := (with - budget.LiveHeap()) / 1024
	t.Logf("%d KB live", live)
	// 127 KB today, 28 of them gob's buffer for a part of 313 songs
	// (api.Ahead: 5000 in 16); the reply whole, 1422.
	if live > 146 {
		t.Errorf("%d KB live, budget 146", live)
	}
}

// TestBudgetLiveCall holds the heap a connection keeps once it enqueued
// 5000 songs, sent in parts as front-ends send them: with the connection,
// less once it closed.
func TestBudgetLiveCall(t *testing.T) {
	if budget.Race {
		t.Skip("the race detector allocates on its own")
	}
	h := newHarness(t, Options{})
	ours, leave := h.serveOne()
	enqueue(t, ours, catalogtest.Decoded(5000))
	go io.Copy(io.Discard, ours) //nolint:errcheck // ends as the pipe closes
	// The call's batch settled.
	h.do(func() {})
	with := budget.LiveHeap()
	leave()
	live := (with - budget.LiveHeap()) / 1024
	t.Logf("%d KB live", live)
	// 116 to 118 KB today, gob's decoder most of it, holding the call and
	// its 5000 IDs (60 KB); the call whole, 1214.
	if live > 136 {
		t.Errorf("%d KB live, budget 136", live)
	}
}

// enqueue greets the daemon over nc and enqueues songs, their details in
// parts ahead of the call, reading frames up to its reply; the encoder
// goes with it, so that only the daemon's side stays.
func enqueue(t *testing.T, nc net.Conn, songs []catalog.Song) {
	t.Helper()
	enc, dec := gob.NewEncoder(nc), gob.NewDecoder(nc)
	hello := api.Hello{Proto: api.Proto, Version: "1.2.3", Role: api.RoleDesktop, PID: 81}
	call := api.Call{Method: api.MethodEnqueue, IDs: &api.IDsArgs{IDs: ids(songs), Songs: songs}}
	frames := []api.Frame{{Hello: &hello}}
	for _, a := range call.Split() {
		frames = append(frames, api.Frame{ID: 1, Ahead: a})
	}
	for _, f := range append(frames, api.Frame{ID: 1, Call: &call}) {
		if err := enc.Encode(&f); err != nil {
			t.Fatal(err)
		}
	}
	readReply(t, dec, 1)
}

// TestBudgetLiveLikes holds the heap a signed-in ytmd keeps once a
// subscriber fetched 5000 liked songs through it and left: the front-ends
// keep the likes.
func TestBudgetLiveLikes(t *testing.T) {
	if budget.Race {
		t.Skip("the race detector allocates on its own")
	}
	h := newHarness(t, Options{})
	h.signedIn()
	h.until("signed in", func() bool { return h.svc.status().SignedIn })
	// A first fetch makes what later ones share, such as gob's types.
	fetchLikedAndLeave(t, h)
	before := budget.LiveHeap()
	fetchLikedAndLeave(t, h)
	live := (budget.LiveHeap() - before) / 1024
	t.Logf("%d KB live", live)
	// -6 to 7 KB today, noise: ytmd keeps none of them.
	if live > 9 {
		t.Errorf("%d KB live, budget 9", live)
	}
}

// fetchLikedAndLeave connects a subscriber that fetches Liked music
// (fetchLiked), then leaves.
func fetchLikedAndLeave(t *testing.T, h *harness) {
	t.Helper()
	ours, leave := h.serveOne()
	fetchLiked(t, h, ours, true)
	leave()
}

// fetchLiked greets the daemon over nc and fetches Liked music, which the
// source answers with 5000 songs, reading frames up to its reply, then a
// ping's; the decoder and songs go with it, so that only the daemon's side
// stays.
func fetchLiked(t *testing.T, h *harness, nc net.Conn, subscribe bool) {
	t.Helper()
	liked := catalogtest.Decoded(5000)
	enc, dec := gob.NewEncoder(nc), gob.NewDecoder(nc)
	hello := api.Hello{Proto: api.Proto, Version: "1.2.3", Role: api.RoleTUI, Subscribe: subscribe, PID: 79}
	call := api.Call{Method: api.MethodPlaylist, Ref: &api.RefArgs{ID: catalog.LikedID}}
	for _, f := range []api.Frame{{Hello: &hello}, {ID: 1, Call: &call}} {
		if err := enc.Encode(&f); err != nil {
			t.Fatal(err)
		}
	}
	h.src.Next(t, nil, "playlist", catalog.LikedID).Reply <- catalogtest.Reply{
		Playlist: catalog.Playlist{ID: catalog.LikedID, Songs: ids(liked)}, Songs: liked,
	}
	readReply(t, dec, 1)
	// A round trip more lets the fetch's goroutine, which held the reply,
	// end: on one P it may not have yet.
	if err := enc.Encode(&api.Frame{ID: 2, Call: &api.Call{Method: api.MethodPing}}); err != nil {
		t.Fatal(err)
	}
	readReply(t, dec, 2)
}

// readReply reads frames up to the whole reply to id.
func readReply(t *testing.T, dec *gob.Decoder, id uint32) {
	t.Helper()
	for {
		var f api.Frame
		if err := dec.Decode(&f); err != nil {
			t.Fatal(err)
		}
		if f.ID == id && f.Reply != nil {
			return
		}
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

// serveOne serves a connection over net.Pipe, returning its end and
// leave, which closes it and waits until the Service dropped it.
func (h *harness) serveOne() (nc net.Conn, leave func()) {
	ours, theirs := net.Pipe()
	served := make(chan struct{})
	go func() {
		h.svc.Serve(theirs)
		close(served)
	}()
	return ours, func() {
		_ = ours.Close()
		<-served
		h.do(func() {}) // its unregister
	}
}
