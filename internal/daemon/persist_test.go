package daemon

import (
	"encoding/json/v2"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/catalog/catalogtest"
	"yt-music-player/internal/ipc"
	"yt-music-player/internal/player"
)

// readState reads the state file once no write is under way.
func (h *harness) readState() savedState {
	h.t.Helper()
	h.svc.store.wait()
	data, err := os.ReadFile(h.svc.store.path)
	if err != nil {
		h.t.Fatal(err)
	}
	var st savedState
	if err := json.Unmarshal(data, &st); err != nil {
		h.t.Fatal(err)
	}
	return st
}

func savedSongs(songs []catalog.Song) []savedSong {
	out := make([]savedSong, len(songs))
	for i, s := range songs {
		out[i] = savedSong(s)
	}
	return out
}

func TestSaveAndRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.json")
	// Saving after a change waits longer than the test.
	h := newHarness(t, Options{State: path, SaveDelay: time.Hour})
	c := h.connect(true)
	c.event()
	queue := songs(3)
	// A new song saves at once.
	c.call(playAll(queue, 1))
	h.ready()
	st := h.readState()
	want := savedState{
		V: stateVersion, Queue: ids(queue), Index: 1, Order: []int{0, 1, 2},
		Volume: 0.7, Songs: savedSongs(queue),
	}
	if !reflect.DeepEqual(st, want) {
		t.Errorf("saved %+v, want %+v", st, want)
	}
	// So does a seek, and a pause, with the position.
	c.call(api.Call{Method: api.MethodSeek, Seek: &api.SeekArgs{Seconds: 50}})
	c.ping()
	if st := h.readState(); st.Position != 50 {
		t.Errorf("saved at %v after a seek, want 50", st.Position)
	}
	h.do(func() { h.fake.Pos = 61 })
	c.call(api.Call{Method: api.MethodPause})
	c.ping()
	if st := h.readState(); st.Position != 61 {
		t.Errorf("saved at %v after a pause, want 61", st.Position)
	}
	// The queue, as edited, waits for the delay.
	c.call(api.Call{Method: api.MethodCycleRepeat})
	c.call(api.Call{Method: api.MethodRemove, Index: &api.IndexArgs{Index: 0}})
	c.ping()
	if st := h.readState(); len(st.Queue) != 3 || st.Repeat != player.RepeatOff {
		t.Errorf("saved %+v before the delay", st)
	}
	h.svc.Shutdown()

	again := newHarness(t, Options{State: path})
	snap := again.connect(true).event()
	wantState := api.State{
		Position: 61, Duration: 200, Volume: 0.7, Repeat: api.RepeatAll, Current: "s1",
	}
	if *snap.State != wantState || !slices.Equal(snap.Queue.IDs, []string{"s1", "s2"}) ||
		!reflect.DeepEqual(snap.Songs, queue[1:]) {
		t.Errorf("restored %+v, %+v, want %+v", *snap.State, snap, wantState)
	}
}

func TestDelayedSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.json")
	h := newHarness(t, Options{State: path, SaveDelay: 100 * time.Millisecond, Seed: songs(2)})
	c := h.connect(false)
	c.call(api.Call{Method: api.MethodSetVolume, Volume: &api.VolumeArgs{Volume: 0.3}})
	c.call(api.Call{Method: api.MethodToggleShuffle})
	c.ping()
	h.svc.store.wait()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("saved before the delay: %v", err)
	}
	deadline := time.Now().Add(wait)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not saved after the delay")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st := h.readState(); st.Volume != 0.3 || !st.Shuffled || len(st.Songs) != 2 {
		t.Errorf("saved %+v", st)
	}
}

func TestBadState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.json")
	for _, bad := range []string{"{not json", `{"v":2,"queue":["a"]}`} {
		if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		h := newHarness(t, Options{State: path, Seed: songs(2)})
		snap := h.connect(true).event()
		if len(snap.Queue.IDs) != 0 || snap.State.Volume != 0.7 {
			t.Errorf("from %q: %+v, %+v, want empty", bad, snap.Queue, snap.State)
		}
		if got, err := os.ReadFile(path + ".bad"); err != nil || string(got) != bad {
			t.Errorf("kept aside %q, %v", got, err)
		}
		h.close()
	}
}

// A state saved before the queue held each song once loads with each
// once, at the same song.
func TestStateRepeats(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.json")
	queue := songs(3)
	data, err := json.Marshal(savedState{
		V: stateVersion, Queue: []string{"s0", "s1", "s0", "s2"}, Order: []int{2, 3, 0, 1}, Index: 3,
		Shuffled: true, Volume: 0.5, Songs: savedSongs(queue),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, Options{State: path})
	snap := h.connect(true).event()
	if !slices.Equal(snap.Queue.IDs, []string{"s0", "s2", "s1"}) || snap.State.Current != "s1" || snap.State.Index != 2 || len(snap.Songs) != 3 {
		t.Errorf("loaded queue %q, %d songs, state %+v", snap.Queue.IDs, len(snap.Songs), snap.State)
	}
}

func TestHandoff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.json")
	h := newHarness(t, Options{State: path})
	other := h.connect(true)
	other.event()
	c := h.connectAs(api.Hello{Proto: api.Proto, Role: api.RoleTUI, PID: 4242})
	c.call(playAll(songs(2), 0))
	h.ready()
	h.do(func() { h.fake.Pos = 20 })
	c.call(api.Call{Method: api.MethodHandoff, Hand: &api.HandoffArgs{Wait: 5 * time.Second}})
	ev := other.eventWith(func(ev api.Event) bool { return ev.Handoff != nil })
	if *ev.Handoff != (api.HandoffEvent{By: 4242, Wait: 5 * time.Second}) {
		t.Errorf("handoff event %+v", *ev.Handoff)
	}
	other.closed()
	c.closed()
	<-h.svc.Done()
	st := h.readState()
	if !st.ResumePlaying || !st.ResumeBefore.Equal(h.clock.Now().Add(35*time.Second)) || st.Position != 20 {
		t.Errorf("saved %+v", st)
	}

	// Started in time, the next daemon plays on, once.
	next := newHarness(t, Options{State: path})
	next.clock.set(h.clock.Now().Add(30 * time.Second))
	nc := next.connect(true)
	if snap := nc.event(); snap.State.Playing {
		t.Error("playing before Start")
	}
	next.svc.Start()
	if st := nc.state(); !st.Playing || st.Position != 20 {
		t.Errorf("resumed %+v", st)
	}
	next.src.Take(t, nil, "account", "library", "playlist LM")
	if st := next.readState(); st.ResumePlaying {
		t.Errorf("resumePlaying kept: %+v", st)
	}
}

// TestHandoffUnlocksFirst: the asker's next ytmd finds the lock free while
// a stalled front-end still holds up the flush.
func TestHandoffUnlocksFirst(t *testing.T) {
	path := filepath.Join(shortDir(t), "ytmd.lock")
	lock, err := ipc.TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, Options{})
	h.svc.lock = lock
	// Greets, then never reads: the daemon's hello to it never leaves.
	ours, theirs := net.Pipe()
	t.Cleanup(func() { _ = ours.Close() })
	go h.svc.Serve(theirs)
	if err := ipc.NewConn(ours).Send(api.Frame{Hello: &api.Hello{Proto: api.Proto, Role: api.RoleTUI}}); err != nil {
		t.Fatal(err)
	}
	c := h.connect(false)
	for n := 0; n < 2; {
		h.do(func() { n = len(h.svc.conns) })
	}
	c.call(api.Call{Method: api.MethodHandoff, Hand: &api.HandoffArgs{Wait: 5 * time.Second}})
	c.closed()
	next, err := ipc.TryLock(path)
	if err != nil {
		t.Fatalf("lock after the hand-off: %v", err)
	}
	_ = next.Unlock()
}

func TestHandoffTooLate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.json")
	h := newHarness(t, Options{State: path})
	saved := h.clock.Now()
	h.do(func() {
		h.svc.player.PlayAll([]string{"a"}, 0)
		st := h.svc.saved()
		st.ResumePlaying, st.ResumeBefore = true, saved.Add(time.Minute)
		h.svc.store.save(st)
		h.svc.store.wait()
	})
	h.close()
	late := newHarness(t, Options{State: path})
	late.clock.set(saved.Add(2 * time.Minute))
	late.svc.Start()
	late.src.Take(t, nil, "account", "library", "playlist LM")
	if st := late.connect(true).event().State; st.Playing {
		t.Errorf("resumed past resumeBefore: %+v", st)
	}
}

// Of two hand-offs at once, only the first is answered; the other hears of
// it as the other connections do.
func TestTwoHandoffs(t *testing.T) {
	h := newHarness(t, Options{Seed: songs(1)})
	a := h.connectAs(api.Hello{Proto: api.Proto, Role: api.RoleDesktop, Subscribe: true, PID: 1})
	b := h.connectAs(api.Hello{Proto: api.Proto, Role: api.RoleTUI, Subscribe: true, PID: 2})
	a.event()
	b.event()
	hand := api.Call{Method: api.MethodHandoff, Hand: &api.HandoffArgs{Wait: time.Second}}
	a.send(hand)
	b.send(hand)
	replies, events := 0, 0
	for _, c := range []*client{a, b} {
		for f := range c.frames {
			switch {
			case f.Reply != nil:
				replies++
			case f.Event != nil && f.Event.Handoff != nil:
				events++
			}
		}
	}
	if replies != 1 || events != 1 {
		t.Errorf("%d replies and %d hand-off events, want 1 each", replies, events)
	}
	<-h.svc.Done()
}

// shortDir is a temporary directory short enough for a socket.
func shortDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if len(dir) < 60 {
		return dir
	}
	dir, err := os.MkdirTemp("", "ytmd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// TestOpen: the lock, then the state, then the socket.
func TestOpen(t *testing.T) {
	dir := shortDir(t)
	paths, err := ipc.Resolve(ipc.Roots{Config: dir, Cache: dir, Runtime: dir, Temp: dir, UID: os.Getuid()}, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.Prepare(); err != nil {
		t.Fatal(err)
	}
	queue := songs(2)
	data, err := json.Marshal(savedState{V: stateVersion, Queue: ids(queue), Order: []int{0, 1}, Index: 1, Volume: 0.5, Songs: savedSongs(queue)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.State, data, 0o600); err != nil {
		t.Fatal(err)
	}
	// A daemon that crashed left its socket.
	crashed, err := net.ListenUnix("unix", &net.UnixAddr{Name: paths.Socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	crashed.SetUnlinkOnClose(false)
	_ = crashed.Close()

	opts := func() Options { return Options{Source: catalogtest.New()} }
	svc, err := Open(paths, opts())
	if err != nil {
		t.Fatal(err)
	}
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			svc.Shutdown()
		}
	})
	// Held, the lock keeps a second daemon from touching anything.
	if _, err := Open(paths, opts()); !errors.Is(err, ipc.ErrLocked) {
		t.Fatalf("second Open: %v, want ErrLocked", err)
	}
	svc.Start()
	nc, err := ipc.Dial(t.Context(), paths.Socket)
	if err != nil {
		t.Fatal(err)
	}
	c := newClient(t, nc, api.Hello{Proto: api.Proto, Role: api.RoleTUI, Subscribe: true})
	if snap := c.event(); snap.State.Current != "s1" || snap.State.Volume != 0.5 || len(snap.Songs) != 2 {
		t.Errorf("first snapshot %+v, want the state loaded", snap.State)
	}

	svc.Shutdown()
	stopped = true
	c.closed()
	if _, err := os.Lstat(paths.Socket); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket left: %v", err)
	}
	lock, err := ipc.TryLock(paths.Lock)
	if err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	_ = lock.Unlock()
}
