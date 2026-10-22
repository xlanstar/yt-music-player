package daemon

import (
	"errors"
	"net"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/audio"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/catalog/catalogtest"
	"yt-music-player/internal/ipc"
	"yt-music-player/internal/notice"
	"yt-music-player/internal/stream"
	"yt-music-player/internal/version"
)

func TestHelloAndSnapshot(t *testing.T) {
	seed := songs(3)
	h := newHarness(t, Options{Seed: seed})
	c := h.connect(true)
	want := api.Hello{Proto: api.Proto, Version: version.Version, Role: api.RoleDaemon, PID: os.Getpid()}
	if c.hello != want {
		t.Errorf("hello %+v, want %+v", c.hello, want)
	}
	ev := c.event()
	if ev.State == nil || ev.Queue == nil {
		t.Fatalf("snapshot %+v lacks the state or the queue", ev)
	}
	if !slices.Equal(ev.Queue.IDs, ids(seed)) || !reflect.DeepEqual(ev.Songs, seed) {
		t.Errorf("queue %q, songs %+v", ev.Queue.IDs, ev.Songs)
	}
	wantState := api.State{Volume: 0.7, Repeat: api.RepeatOff, Current: "s0", Duration: 200}
	if *ev.State != wantState {
		t.Errorf("state %+v, want %+v", *ev.State, wantState)
	}
	// Not fetched yet, so not told.
	if ev.Account != nil || ev.Library != nil || ev.Likes != nil {
		t.Errorf("snapshot tells the account before any fetch: %+v", ev)
	}
	c.none(50 * time.Millisecond)

	// A CLI asks for no events.
	cli := h.connect(false)
	cli.ping()
	cli.none(50 * time.Millisecond)
}

func TestBadHello(t *testing.T) {
	h := newHarness(t, Options{})
	for _, first := range []api.Frame{
		{ID: 1, Call: &api.Call{Method: api.MethodPing}},
		{Hello: &api.Hello{Proto: api.Proto}}, // no role
	} {
		ours, theirs := net.Pipe()
		go h.svc.Serve(theirs)
		c := ipc.NewConn(ours)
		if err := c.Send(first); err != nil {
			t.Fatal(err)
		}
		var f api.Frame
		if err := c.Receive(&f); err == nil {
			t.Errorf("after %+v, got %+v, want the connection closed", first, f)
		}
		_ = c.Close()
	}
}

func TestOtherProto(t *testing.T) {
	h := newHarness(t, Options{Seed: songs(2)})
	c := h.connectAs(api.Hello{Proto: api.Proto + 1, Version: "9.0.0", Role: api.RoleTUI, Subscribe: true, PID: 5})
	c.ping()
	r := c.reply(c.send(api.Call{Method: api.MethodStatus}))
	if r.Err == nil || r.Status != nil {
		t.Errorf("status for another Proto: %+v", r)
	}
	// No snapshot, no events.
	c.none(50 * time.Millisecond)
}

func TestInvalidCall(t *testing.T) {
	h := newHarness(t, Options{Seed: songs(2)})
	c := h.connect(false)
	for _, cl := range []api.Call{
		{Method: api.MethodSeek},
		{Method: 999},
		{Method: api.MethodPlay, IDs: &api.IDsArgs{}},
		{Method: api.MethodPlayAll, IDs: &api.IDsArgs{IDs: []string{"a"}, Start: 1}},
	} {
		r := c.reply(c.send(cl))
		if !errors.Is(r.Err.Err(), api.ErrInternal) {
			t.Errorf("%+v: reply %+v, want an error", cl, r)
		}
	}
}

func TestControls(t *testing.T) {
	h := newHarness(t, Options{})
	c := h.connect(true)
	c.event() // the snapshot
	queue := songs(4)
	r := c.reply(c.send(playAll(queue, 1)))
	if r.Err != nil {
		t.Fatal(r.Err.Err())
	}
	// The reply comes before the events it causes.
	if len(c.events) > 0 {
		t.Fatalf("events %+v before the reply", c.events)
	}
	ev := c.eventWith(hasState)
	if ev.Queue == nil || !slices.Equal(ev.Queue.IDs, ids(queue)) || !reflect.DeepEqual(ev.Songs, queue) {
		t.Fatalf("queue event %+v", ev)
	}
	if st := ev.State; !st.Playing || !st.Waiting || st.Current != "s1" || st.Index != 1 {
		t.Errorf("state %+v, want s1 playing, waiting", *st)
	}
	h.loaded()
	if got := h.calls(); !slices.Equal(got, []string{"volume 0.7", "load https://audio/s1 at 0", "play"}) {
		t.Errorf("engine calls %q", got)
	}
	h.ready()
	if st := c.state(); st.Waiting || !st.Playing || st.Duration != 200 {
		t.Errorf("ready: %+v", st)
	}

	for _, tc := range []struct {
		call  api.Call
		check func(api.State) bool
	}{
		{api.Call{Method: api.MethodToggle}, func(st api.State) bool { return !st.Playing }},
		{api.Call{Method: api.MethodResume}, func(st api.State) bool { return st.Playing }},
		{api.Call{Method: api.MethodPause}, func(st api.State) bool { return !st.Playing }},
		{api.Call{Method: api.MethodSeek, Seek: &api.SeekArgs{Seconds: 42}}, func(st api.State) bool { return st.Position == 42 }},
		{api.Call{Method: api.MethodSetVolume, Volume: &api.VolumeArgs{Volume: 0.25}}, func(st api.State) bool { return st.Volume == 0.25 }},
		{api.Call{Method: api.MethodToggleMute}, func(st api.State) bool { return st.Muted }},
		{api.Call{Method: api.MethodCycleRepeat}, func(st api.State) bool { return st.Repeat == api.RepeatAll }},
		{api.Call{Method: api.MethodNext}, func(st api.State) bool { return st.Current == "s2" && st.Position == 0 }},
		{api.Call{Method: api.MethodPrev}, func(st api.State) bool { return st.Current == "s1" }},
		{api.Call{Method: api.MethodToggleShuffle}, func(st api.State) bool { return st.Shuffled && st.Current == "s1" }},
		{api.Call{Method: api.MethodToggleShuffle}, func(st api.State) bool { return !st.Shuffled }},
	} {
		c.call(tc.call)
		if st := c.state(); !tc.check(st) {
			t.Errorf("after %s: %+v", tc.call.Method, st)
		}
	}
	if got := h.calls(); !slices.Contains(got, "seek 42") || !slices.Contains(got, "volume 0.25") || !slices.Contains(got, "volume 0") {
		t.Errorf("engine calls %q", got)
	}
}

func TestQueueEdits(t *testing.T) {
	h := newHarness(t, Options{})
	c := h.connect(true)
	c.event()
	queue := songs(3)
	c.call(playAll(queue, 0))
	c.eventWith(hasState)
	extra := []catalog.Song{{ID: "x", Title: "X"}, {ID: "y", Title: "Y"}}
	for _, tc := range []struct {
		call  api.Call
		queue []string
		songs []catalog.Song
	}{
		{api.Call{Method: api.MethodEnqueue, IDs: &api.IDsArgs{IDs: ids(extra), Songs: extra, Next: true}}, []string{"s0", "x", "y", "s1", "s2"}, extra},
		{api.Call{Method: api.MethodMove, Move: &api.MoveArgs{From: 4, To: 0}}, []string{"s2", "s0", "x", "y", "s1"}, nil},
		{api.Call{Method: api.MethodRemove, Index: &api.IndexArgs{Index: 2}}, []string{"s2", "s0", "y", "s1"}, nil},
		{api.Call{Method: api.MethodClearUpNext}, []string{"s2", "s0"}, nil},
		{api.Call{Method: api.MethodEnqueue, IDs: &api.IDsArgs{IDs: []string{"x"}, Songs: extra[:1]}}, []string{"s2", "s0", "x"}, extra[:1]},
	} {
		c.call(tc.call)
		ev := c.eventWith(func(ev api.Event) bool { return ev.Queue != nil })
		if !slices.Equal(ev.Queue.IDs, tc.queue) || !reflect.DeepEqual(ev.Songs, tc.songs) {
			t.Errorf("after %s: queue %q, new songs %+v; want %q, %+v", tc.call.Method, ev.Queue.IDs, ev.Songs, tc.queue, tc.songs)
		}
		if ev.State == nil || ev.State.Current != "s0" || tc.queue[ev.State.Index] != "s0" {
			t.Errorf("after %s: state %+v", tc.call.Method, ev.State)
		}
	}
	// The daemon keeps only the queue's songs.
	h.do(func() {
		if got := ids(h.svc.cat.Songs); !slices.Equal(slices.Sorted(slices.Values(got)), []string{"s0", "s2", "x"}) {
			t.Errorf("catalog %q", got)
		}
	})
	// A new subscriber gets all of them.
	late := h.connect(true)
	ev := late.event()
	if len(ev.Songs) != 3 || !slices.Equal(ev.Queue.IDs, []string{"s2", "s0", "x"}) {
		t.Errorf("snapshot %+v", ev)
	}
}

// The queue holds each song once, whatever a front-end sends.
func TestQueueOnce(t *testing.T) {
	h := newHarness(t, Options{})
	c := h.connect(true)
	c.event()
	queue := songs(3)
	c.call(playAll([]catalog.Song{queue[0], queue[1], queue[0], queue[2], queue[1]}, 2))
	ev := c.eventWith(hasState)
	if !slices.Equal(ev.Queue.IDs, []string{"s0", "s1", "s2"}) || ev.State.Current != "s0" || ev.State.Index != 0 {
		t.Errorf("repeats played: queue %q, state %+v", ev.Queue.IDs, ev.State)
	}
	if len(ev.Songs) != 3 {
		t.Errorf("repeats played: songs %+v", ev.Songs)
	}

	x := catalog.Song{ID: "x", Title: "X"}
	for _, tc := range []struct {
		name  string
		args  api.IDsArgs
		queue []string
		songs []catalog.Song
	}{
		{"add queued", api.IDsArgs{IDs: []string{"s1", "x", "x"}, Songs: []catalog.Song{queue[1], x, x}}, []string{"s0", "s1", "s2", "x"}, []catalog.Song{x}},
		{"play queued next", api.IDsArgs{IDs: []string{"x", "s0", "s2"}, Songs: []catalog.Song{x, queue[0], queue[2]}, Next: true}, []string{"s0", "x", "s2", "s1"}, nil},
	} {
		c.call(api.Call{Method: api.MethodEnqueue, IDs: &tc.args})
		ev := c.eventWith(func(ev api.Event) bool { return ev.Queue != nil })
		if !slices.Equal(ev.Queue.IDs, tc.queue) || !reflect.DeepEqual(ev.Songs, tc.songs) {
			t.Errorf("%s: queue %q, new songs %+v; want %q, %+v", tc.name, ev.Queue.IDs, ev.Songs, tc.queue, tc.songs)
		}
		if ev.State == nil || ev.State.Current != "s0" || ev.State.Index != 0 {
			t.Errorf("%s: state %+v", tc.name, ev.State)
		}
	}

	// Adding only queued songs changes nothing, so tells nothing.
	c.call(api.Call{Method: api.MethodEnqueue, IDs: &api.IDsArgs{IDs: []string{"s2", "x", "s0"}}})
	c.ping()
	for _, ev := range c.events {
		if ev.Queue != nil {
			t.Errorf("adding queued songs told queue %q", ev.Queue.IDs)
		}
	}
	h.do(func() {
		if got := slices.Sorted(slices.Values(ids(h.svc.cat.Songs))); !slices.Equal(got, []string{"s0", "s1", "s2", "x"}) {
			t.Errorf("catalog %q", got)
		}
		if got := h.svc.player.Queued(); len(got) != 4 {
			t.Errorf("queued %q", got)
		}
	})
	// A new subscriber gets each song once.
	late := h.connect(true)
	ev = late.event()
	if !slices.Equal(ev.Queue.IDs, []string{"s0", "x", "s2", "s1"}) || len(ev.Songs) != 4 {
		t.Errorf("snapshot queue %q, %d songs", ev.Queue.IDs, len(ev.Songs))
	}
}

func TestPlayOne(t *testing.T) {
	h := newHarness(t, Options{Seed: songs(3)})
	c := h.connect(true)
	c.event()
	song := catalog.Song{ID: "new", Title: "New"}
	c.call(api.Call{Method: api.MethodPlay, IDs: &api.IDsArgs{IDs: []string{"new"}, Songs: []catalog.Song{song}}})
	ev := c.eventWith(hasState)
	if !slices.Equal(ev.Queue.IDs, []string{"s0", "new", "s1", "s2"}) || ev.State.Current != "new" || !ev.State.Playing {
		t.Errorf("event %+v, state %+v", ev, ev.State)
	}
	// Playing the current song pauses it.
	c.call(api.Call{Method: api.MethodPlay, IDs: &api.IDsArgs{IDs: []string{"new"}}})
	if st := c.state(); st.Playing || st.Current != "new" {
		t.Errorf("state %+v", st)
	}
}

func TestStatus(t *testing.T) {
	seed := songs(3)
	h := newHarness(t, Options{Seed: seed})
	c := h.connect(false)
	r := c.call(api.Call{Method: api.MethodStatus})
	want := api.Status{
		Version: version.Version, Proto: api.Proto, Song: seed[0], Length: 3,
		State: api.State{Volume: 0.7, Repeat: api.RepeatOff, Current: "s0", Duration: 200},
	}
	if r.Status == nil || !reflect.DeepEqual(*r.Status, want) {
		t.Errorf("status %+v, want %+v", r.Status, want)
	}
}

// State goes out on changes but the moving position, and on jumps.
func TestStateCoalescing(t *testing.T) {
	h := newHarness(t, Options{})
	c := h.connect(true)
	c.event()
	c.call(playAll(songs(2), 0))
	c.eventWith(hasState)
	h.ready()
	c.state()
	// Played on as the clock says: no news.
	h.clock.advance(10 * time.Second)
	h.do(func() { h.fake.Pos = 10 })
	c.ping()
	c.none(30 * time.Millisecond)
	// Off by more than the slack: a seek, say from a media key.
	h.do(func() { h.fake.Command(audio.CommandSeek, 100) })
	if st := c.state(); st.Position != 100 {
		t.Errorf("after a seek: %+v", st)
	}
	// A stall stops the clock, and says so.
	h.do(func() { h.fake.Emit(audio.StreamBuffering) })
	if st := c.state(); !st.Waiting {
		t.Errorf("stalled: %+v", st)
	}
	h.clock.advance(5 * time.Second)
	c.ping()
	c.none(30 * time.Millisecond)
	h.do(func() { h.fake.Emit(audio.StreamPlaying) })
	if st := c.state(); st.Waiting || st.Position != 100 {
		t.Errorf("playing again: %+v", st)
	}
}

func TestNotice(t *testing.T) {
	h := newHarness(t, Options{})
	c := h.connect(true)
	c.event()
	c.call(playAll(songs(1), 0))
	c.eventWith(hasState)
	h.loaded()
	h.do(func() { h.fake.Fail(stream.ErrUnplayable) })
	ev := c.eventWith(func(ev api.Event) bool { return ev.Notice != nil })
	if n := ev.Notice; n.Kind != notice.KindFailure || n.Topic != notice.TopicPlayback || !slices.Equal(n.Args, []string{"Song s0"}) {
		t.Errorf("notice %+v", n)
	}
}

func TestCatalogRequests(t *testing.T) {
	h := newHarness(t, Options{})
	c := h.connect(false)
	id := c.send(api.Call{Method: api.MethodSearch, Query: &api.QueryArgs{Query: "lofi"}})
	results := catalog.Results{Songs: songs(2)}
	h.src.Next(t, nil, "search", "lofi").Reply <- catalogtest.Reply{Results: results}
	if r := c.reply(id); r.Results == nil || !reflect.DeepEqual(*r.Results, results) {
		t.Errorf("search reply %+v", r)
	}
	id = c.send(api.Call{Method: api.MethodAlbum, Ref: &api.RefArgs{ID: "MPREb_x"}})
	h.src.Next(t, nil, "album", "MPREb_x").Reply <- catalogtest.Reply{Err: catalog.ErrNotFound}
	if r := c.reply(id); !errors.Is(r.Err.Err(), catalog.ErrNotFound) {
		t.Errorf("album reply %+v, want not found", r)
	}
	for _, tc := range []struct {
		call     api.Call
		kind, id string
		reply    catalogtest.Reply
		has      func(api.Reply) bool
	}{
		{api.Call{Method: api.MethodPlaylist, Ref: &api.RefArgs{ID: "PL"}}, "playlist", "PL", catalogtest.Reply{Songs: songs(1)}, func(r api.Reply) bool { return r.Playlist != nil && len(r.Playlist.Songs) == 1 }},
		{api.Call{Method: api.MethodHome}, "home", "", catalogtest.Reply{}, func(r api.Reply) bool { return r.Home != nil }},
		{api.Call{Method: api.MethodExplore}, "explore", "", catalogtest.Reply{}, func(r api.Reply) bool { return r.Explore != nil }},
		{api.Call{Method: api.MethodAccount}, "account", "", catalogtest.Reply{Account: catalog.Account{Name: "Ada"}}, func(r api.Reply) bool { return r.Account != nil && r.Account.Name == "Ada" }},
		{api.Call{Method: api.MethodLibrary}, "library", "", catalogtest.Reply{}, func(r api.Reply) bool { return r.Library != nil }},
	} {
		id := c.send(tc.call)
		h.src.Next(t, nil, tc.kind, tc.id).Reply <- tc.reply
		if r := c.reply(id); !tc.has(r) {
			t.Errorf("%s reply %+v", tc.call.Method, r)
		}
	}
}

func TestCancel(t *testing.T) {
	h := newHarness(t, Options{})
	c := h.connect(false)
	id := c.send(api.Call{Method: api.MethodSearch, Query: &api.QueryArgs{Query: "slow"}})
	search := h.src.Next(t, nil, "search", "slow")
	if err := c.c.Send(api.Frame{ID: id, Cancel: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-search.Ctx.Done():
	case <-time.After(wait):
		t.Fatal("Cancel left the request running")
	}
	search.Reply <- catalogtest.Reply{}
	// Canceled, it gets no reply.
	c.ping()
	c.none(30 * time.Millisecond)

	// The connection's end cancels its requests.
	c.send(api.Call{Method: api.MethodHome})
	home := h.src.Next(t, nil, "home", "")
	_ = c.c.Close()
	select {
	case <-home.Ctx.Done():
	case <-time.After(wait):
		t.Fatal("disconnecting left the request running")
	}
	home.Reply <- catalogtest.Reply{}
}

// A subscriber that reads nothing loses its connection; the loop and the
// others go on.
func TestBackpressure(t *testing.T) {
	h := newHarness(t, Options{Seed: songs(1)})
	ours, theirs := net.Pipe()
	go h.svc.Serve(theirs)
	stuck := ipc.NewConn(ours)
	t.Cleanup(func() { _ = stuck.Close() })
	hello := api.Hello{Proto: api.Proto, Role: api.RoleDesktop, Subscribe: true}
	if err := stuck.Send(api.Frame{Hello: &hello}); err != nil {
		t.Fatal(err)
	}
	c := h.connect(false)
	// Past the seed's s0, so that each changes the queue.
	extra := songs(ipc.QueueLen*2 + 1)[1:]
	for i := range extra {
		// Each makes a Queue event, which nothing coalesces.
		c.call(api.Call{Method: api.MethodEnqueue, IDs: &api.IDsArgs{IDs: ids(extra[i : i+1])}})
	}
	deadline := time.After(wait)
	for {
		var n int
		h.do(func() { n = len(h.svc.conns) })
		if n == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("%d connections, want the stuck one dropped", n)
		case <-time.After(10 * time.Millisecond):
		}
	}
	c.ping()
}

func TestAccount(t *testing.T) {
	h := newHarness(t, Options{})
	c := h.connect(true)
	c.event()
	h.svc.Start()
	calls := h.src.Take(t, nil, "account", "library", "playlist LM")
	ada := catalog.Account{Name: "Ada"}
	lib := catalog.Library{Songs: songs(2)}
	liked := songs(2)
	calls["account"].Reply <- catalogtest.Reply{Account: ada}
	calls["library"].Reply <- catalogtest.Reply{Library: lib}
	calls["playlist LM"].Reply <- catalogtest.Reply{Err: errors.New("offline")}
	got := map[string]bool{}
	for len(got) < 3 {
		ev := c.event()
		switch {
		case ev.Account != nil:
			got["account"] = ev.Account.Account == ada && ev.Account.SignedIn
		case ev.Library != nil:
			got["library"] = reflect.DeepEqual(*ev.Library, lib)
		case ev.Failed != nil:
			got["failed"] = ev.Failed.Part == api.PartLiked && errors.Is(ev.Failed.Err.Err(), api.ErrInternal)
		}
	}
	for k, ok := range got {
		if !ok {
			t.Errorf("%s event wrong", k)
		}
	}

	// A new subscriber hears of the failure too, after its snapshot.
	early := h.connect(true)
	if snap := early.event(); snap.Account == nil || snap.Library == nil || snap.Likes != nil {
		t.Errorf("snapshot %+v", snap)
	}
	if f := early.event().Failed; f == nil || f.Part != api.PartLiked {
		t.Errorf("failed %+v after the snapshot", f)
	}

	// Retry, as the Refresh of a page's Retry.
	c.call(api.Call{Method: api.MethodRefresh, Part: &api.PartArgs{Part: api.PartLiked}})
	h.src.Next(t, nil, "playlist", "LM").Reply <- catalogtest.Reply{Playlist: catalog.Playlist{Songs: ids(liked)}, Songs: liked}
	ev := c.eventWith(func(ev api.Event) bool { return ev.Likes != nil })
	if l := ev.Likes; !l.Full || !slices.Equal(l.IDs, ids(liked)) || !reflect.DeepEqual(l.Songs, liked) {
		t.Errorf("likes %+v", l)
	}

	// A like's reply comes before its change.
	id := c.send(api.Call{Method: api.MethodLike, Like: &api.LikeArgs{ID: "s9", Liked: true}})
	h.src.Next(t, nil, "like", "+s9").Reply <- catalogtest.Reply{}
	if r := c.reply(id); r.Err != nil || len(c.events) > 0 {
		t.Fatalf("like reply %+v after events %+v", r, c.events)
	}
	if l := c.event().Likes; l == nil || l.Full || !slices.Equal(l.Added, []string{"s9"}) || l.Songs != nil {
		t.Errorf("like event %+v", l)
	}

	// A like describing its song tells the others of it.
	s8 := catalog.Song{ID: "s8", Title: "Song s8", Artist: "Artist", Seconds: 200}
	id = c.send(api.Call{Method: api.MethodLike, Like: &api.LikeArgs{ID: "s8", Liked: true, Song: &s8}})
	h.src.Next(t, nil, "like", "+s8").Reply <- catalogtest.Reply{}
	if r := c.reply(id); r.Err != nil {
		t.Fatalf("like reply %+v", r)
	}
	if l := c.event().Likes; l == nil || !slices.Equal(l.Added, []string{"s8"}) || !reflect.DeepEqual(l.Songs, []catalog.Song{s8}) {
		t.Errorf("like event %+v", l)
	}
	other := s8
	other.ID = "s7"
	if r := c.reply(c.send(api.Call{Method: api.MethodLike, Like: &api.LikeArgs{ID: "s8", Liked: true, Song: &other}})); r.Err == nil {
		t.Errorf("like describing another song: %+v", r)
	}

	// A new subscriber learns all of it.
	late := h.connect(true)
	snap := late.event()
	if snap.Account == nil || snap.Account.Account != ada || snap.Library == nil || snap.Likes == nil ||
		!snap.Likes.Full || !slices.Equal(snap.Likes.IDs, []string{"s8", "s9", "s0", "s1"}) ||
		!reflect.DeepEqual(snap.Likes.Songs, append([]catalog.Song{s8}, liked...)) {
		t.Errorf("snapshot %+v", snap)
	}
	late.none(30 * time.Millisecond)

	// Signed out by a like: its reply first, then the status.
	id = c.send(api.Call{Method: api.MethodLike, Like: &api.LikeArgs{ID: "s1"}})
	h.src.Next(t, nil, "like", "-s1").Reply <- catalogtest.Reply{Err: catalog.ErrSignedOut}
	if r := c.reply(id); !errors.Is(r.Err.Err(), catalog.ErrSignedOut) || len(c.events) > 0 {
		t.Fatalf("like reply %+v after %+v", r, c.events)
	}
	if a := c.event().Account; a == nil || a.SignedIn {
		t.Errorf("account event %+v, want signed out", a)
	}
}

// Songs too many for one frame go ahead of the event naming them, in
// events of songs alone, which every Proto 1 front-end keeps.
func TestLongLists(t *testing.T) {
	h := newHarness(t, Options{})
	c := h.connect(true)
	c.event()
	liked := catalogtest.Decoded(1000)
	h.startLiking(liked)
	ev, _, ahead := c.songsAhead(func(ev api.Event) bool { return ev.Likes != nil && ev.Likes.Full })
	if !slices.Equal(ev.Likes.IDs, ids(liked)) || ev.Likes.Songs != nil || !reflect.DeepEqual(ahead, liked) {
		t.Errorf("likes %d IDs, %d songs, %d ahead", len(ev.Likes.IDs), len(ev.Likes.Songs), len(ahead))
	}

	queue := songs(1000)
	c.call(playAll(queue, 0))
	ev, ahead, _ = c.songsAhead(func(ev api.Event) bool { return ev.Queue != nil })
	if !slices.Equal(ev.Queue.IDs, ids(queue)) || ev.Songs != nil || !reflect.DeepEqual(ahead, queue) {
		t.Errorf("queue %d IDs, %d songs, %d ahead", len(ev.Queue.IDs), len(ev.Songs), len(ahead))
	}

	late := h.connect(true)
	snap, queued, likedAhead := late.songsAhead(func(ev api.Event) bool { return ev.Queue != nil })
	byID := func(a, b catalog.Song) int { return strings.Compare(a.ID, b.ID) }
	slices.SortFunc(queued, byID)
	slices.SortFunc(queue, byID)
	if !slices.Equal(snap.Queue.IDs, ids(songs(1000))) || snap.Songs != nil || !reflect.DeepEqual(queued, queue) {
		t.Errorf("snapshot's queue %d IDs, %d songs, %d ahead", len(snap.Queue.IDs), len(snap.Songs), len(queued))
	}
	if l := snap.Likes; !l.Full || !slices.Equal(l.IDs, ids(liked)) || l.Songs != nil || !reflect.DeepEqual(likedAhead, liked) {
		t.Errorf("snapshot's likes %d IDs, %d songs, %d ahead", len(l.IDs), len(l.Songs), len(likedAhead))
	}
}

// carry sends each long list of an event whole ahead, in at most
// maxSongFrames frames.
func TestCarry(t *testing.T) {
	for _, tc := range []struct{ n, frames, kept int }{
		{0, 0, 0},
		{songsPerFrame, 0, songsPerFrame},
		{songsPerFrame + 1, 2, 0},
		{songsPerFrame * maxSongFrames, maxSongFrames, 0},
		{100_000, maxSongFrames, 0},
	} {
		list := songs(tc.n)
		ev := api.Event{Songs: list, Likes: &api.Likes{Songs: list}}
		ahead := carry(nil, &ev)
		var queued, liked []catalog.Song
		for _, f := range ahead {
			queued = append(queued, f.Event.Songs...)
			if f.Event.Likes != nil {
				liked = append(liked, f.Event.Likes.Songs...)
			}
		}
		queued, liked = append(queued, ev.Songs...), append(liked, ev.Likes.Songs...)
		if len(ahead) != 2*tc.frames || len(ev.Songs) != tc.kept || len(ev.Likes.Songs) != tc.kept ||
			!slices.Equal(queued, list) || !slices.Equal(liked, list) {
			t.Errorf("%d songs: %d frames, %d and %d kept, %d and %d sent; want 2×%d frames, %d kept",
				tc.n, len(ahead), len(ev.Songs), len(ev.Likes.Songs), len(queued), len(liked), tc.frames, tc.kept)
		}
	}
}

// Started, ytmd fetches who is signed in, for `ytm status`; the library
// and likes once, for the first subscriber.
func TestLibraryForSubscribers(t *testing.T) {
	h := newHarness(t, Options{})
	cli := h.connect(false)
	h.svc.Start()
	ada := catalog.Account{Name: "Ada"}
	h.src.Next(t, nil, "account", "").Reply <- catalogtest.Reply{Account: ada}
	h.until("signed in", func() bool { return h.svc.status().SignedIn })
	if st := cli.call(api.Call{Method: api.MethodStatus}).Status; st.Account != ada {
		t.Errorf("status %+v", st)
	}
	h.src.None(t, nil, 30*time.Millisecond)

	first := h.connect(true)
	if snap := first.event(); snap.Account == nil || snap.Library != nil || snap.Likes != nil {
		t.Errorf("first snapshot %+v", snap)
	}
	calls := h.src.Take(t, nil, "library", "playlist LM")
	// One connecting meanwhile hears of them as the first does.
	second := h.connect(true)
	second.event()
	h.src.None(t, nil, 30*time.Millisecond)
	lib := catalog.Library{Songs: songs(2)}
	liked := songs(3)
	calls["library"].Reply <- catalogtest.Reply{Library: lib}
	calls["playlist LM"].Reply <- catalogtest.Reply{Playlist: catalog.Playlist{Songs: ids(liked)}, Songs: liked}
	for _, c := range []*client{first, second} {
		var (
			gotLib   *catalog.Library
			gotLikes *api.Likes
		)
		for gotLib == nil || gotLikes == nil {
			ev := c.event()
			if ev.Library != nil {
				gotLib = ev.Library
			}
			if ev.Likes != nil {
				gotLikes = ev.Likes
			}
		}
		if !reflect.DeepEqual(*gotLib, lib) || !slices.Equal(gotLikes.IDs, ids(liked)) {
			t.Errorf("library %+v, likes %+v", gotLib, gotLikes)
		}
	}

	// Later ones, after the others left, find them in their snapshot.
	_ = first.c.Close()
	_ = second.c.Close()
	late := h.connect(true)
	if snap := late.event(); snap.Library == nil || snap.Likes == nil || !slices.Equal(snap.Likes.IDs, ids(liked)) {
		t.Errorf("late snapshot %+v", snap)
	}
	h.src.None(t, nil, 30*time.Millisecond)
}

// Signed out, a subscriber has nothing fetched; a sign-in fetches it all,
// before any subscriber too, which then fetches nothing again.
func TestLibraryAfterSignIn(t *testing.T) {
	t.Run("signed out", func(t *testing.T) {
		h := signedOutHarness(t)
		h.until("signed out", func() bool {
			_, ok := h.svc.acct.Status()
			return ok
		})
		if a := h.connect(true).event().Account; a == nil || a.SignedIn {
			t.Errorf("account %+v, want signed out", a)
		}
		h.src.None(t, nil, 30*time.Millisecond)
	})
	t.Run("signed in without subscribers", func(t *testing.T) {
		h := signedOutHarness(t)
		cli := h.connect(false)
		id := cli.send(signIn("SID=1"))
		h.src.Next(t, nil, "signin", "SID=1").Reply <- catalogtest.Reply{Account: catalog.Account{Name: "Ada"}}
		if r := cli.reply(id); r.Err != nil {
			t.Fatal(r.Err.Err())
		}
		h.src.Take(t, nil, "account", "library", "playlist LM")
		h.connect(true).event()
		h.src.None(t, nil, 30*time.Millisecond)
	})
}

// signedOutHarness is a harness started signed out.
func signedOutHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, Options{})
	h.svc.Start()
	h.src.Next(t, nil, "account", "").Reply <- catalogtest.Reply{Err: catalog.ErrSignedOut}
	return h
}

func TestSignInAndOut(t *testing.T) {
	h := newHarness(t, Options{Seed: songs(2)})
	c := h.connect(true)
	c.event()
	if r := c.reply(c.send(api.Call{Method: api.MethodSignIn, SignIn: &api.SignInArgs{Credentials: " \n"}})); !errors.Is(r.Err.Err(), catalog.ErrInvalidCredentials) {
		t.Errorf("blank sign-in: %+v", r)
	}
	first := c.send(api.Call{Method: api.MethodSignIn, SignIn: &api.SignInArgs{Credentials: "SID=1"}})
	raced := h.src.Next(t, nil, "signin", "SID=1")
	second := c.send(api.Call{Method: api.MethodSignIn, SignIn: &api.SignInArgs{Credentials: "SID=2"}})
	// The sign-in replaced is answered at once.
	if r := c.reply(first); r.Err == nil {
		t.Errorf("replaced sign-in: %+v", r)
	}
	raced.Reply <- catalogtest.Reply{Err: raced.Ctx.Err()}
	ada := catalog.Account{Name: "Ada"}
	h.src.Next(t, nil, "signin", "SID=2").Reply <- catalogtest.Reply{Account: ada}
	if r := c.reply(second); r.Account == nil || *r.Account != ada {
		t.Errorf("sign-in reply %+v", r)
	}
	// Its status came first, as the account orders it; the queue, the
	// previous account's, is gone.
	if a := c.event().Account; a == nil || !a.Switched || a.Account != ada {
		t.Errorf("account event %+v", a)
	}
	if ev := c.eventWith(func(ev api.Event) bool { return ev.Queue != nil }); len(ev.Queue.IDs) != 0 || ev.State.Current != "" {
		t.Errorf("queue after switching accounts: %+v", ev)
	}
	h.src.Take(t, nil, "account", "library", "playlist LM")

	out := c.send(api.Call{Method: api.MethodSignOut})
	signOut := h.src.Next(t, nil, "signout", "")
	again := c.send(api.Call{Method: api.MethodSignOut})
	c.ping()
	signOut.Reply <- catalogtest.Reply{}
	if r := c.reply(out); r.Err != nil {
		t.Errorf("sign-out: %+v", r)
	}
	if r := c.reply(again); r.Err != nil {
		t.Errorf("second sign-out: %+v", r)
	}
}

func signIn(credentials string) api.Call {
	return api.Call{Method: api.MethodSignIn, SignIn: &api.SignInArgs{Credentials: credentials}}
}

func canceled(t *testing.T, call catalogtest.Call, what string) {
	t.Helper()
	select {
	case <-call.Ctx.Done():
	case <-time.After(wait):
		t.Fatalf("%s left the sign-in running", what)
	}
}

func TestCancelSignIn(t *testing.T) {
	h := newHarness(t, Options{Seed: songs(2)})
	c := h.connect(true)
	c.event()

	// Canceled, the sign-in stops and is never answered.
	id := c.send(signIn("SID=1"))
	first := h.src.Next(t, nil, "signin", "SID=1")
	if err := c.c.Send(api.Frame{ID: id, Cancel: true}); err != nil {
		t.Fatal(err)
	}
	canceled(t, first, "Cancel")
	first.Reply <- catalogtest.Reply{Account: catalog.Account{Name: "Bob"}, Err: first.Ctx.Err()}
	c.ping()
	c.none(30 * time.Millisecond)

	// A Cancel of a sign-in replaced by another front-end's leaves that one.
	other := h.connect(false)
	id = c.send(signIn("SID=2"))
	replaced := h.src.Next(t, nil, "signin", "SID=2")
	later := other.send(signIn("SID=3"))
	if r := c.reply(id); r.Err == nil {
		t.Errorf("replaced sign-in: %+v", r)
	}
	current := h.src.Next(t, nil, "signin", "SID=3")
	if err := c.c.Send(api.Frame{ID: id, Cancel: true}); err != nil {
		t.Fatal(err)
	}
	c.ping()
	if current.Ctx.Err() != nil {
		t.Fatal("a Cancel of the sign-in replaced canceled the other's")
	}
	replaced.Reply <- catalogtest.Reply{Err: replaced.Ctx.Err()}
	ada := catalog.Account{Name: "Ada"}
	current.Reply <- catalogtest.Reply{Account: ada}
	if r := other.reply(later); r.Account == nil || *r.Account != ada {
		t.Errorf("sign-in reply %+v", r)
	}
	if a := c.eventWith(func(ev api.Event) bool { return ev.Account != nil }).Account; !a.Switched || a.Account != ada {
		t.Errorf("account event %+v", a)
	}
	h.src.Take(t, nil, "account", "library", "playlist LM")

	// Credentials stored anyway still sign in, as package account has it,
	// which only the Account event tells.
	id = c.send(signIn("SID=4"))
	stored := h.src.Next(t, nil, "signin", "SID=4")
	if err := c.c.Send(api.Frame{ID: id, Cancel: true}); err != nil {
		t.Fatal(err)
	}
	canceled(t, stored, "Cancel")
	grace := catalog.Account{Name: "Grace"}
	stored.Reply <- catalogtest.Reply{Account: grace}
	if a := c.eventWith(func(ev api.Event) bool { return ev.Account != nil }).Account; !a.Switched || a.Account != grace {
		t.Errorf("account event %+v", a)
	}
	h.src.Take(t, nil, "account", "library", "playlist LM")
	c.ping()

	// The connection's end cancels its sign-in.
	other.send(signIn("SID=5"))
	gone := h.src.Next(t, nil, "signin", "SID=5")
	_ = other.c.Close()
	canceled(t, gone, "disconnecting")
	gone.Reply <- catalogtest.Reply{Err: gone.Ctx.Err()}
	c.ping()
	for _, ev := range c.events {
		if ev.Account != nil {
			t.Errorf("account event %+v after a sign-in canceled", ev.Account)
		}
	}
}

func TestSignInWithoutAccounts(t *testing.T) {
	h := &harness{t: t, svc: New(Options{Source: catalogtest.New(t)})}
	t.Cleanup(h.close)
	c := h.connect(false)
	for _, cl := range []api.Call{
		{Method: api.MethodSignIn, SignIn: &api.SignInArgs{Credentials: "SID=1"}},
		{Method: api.MethodSignOut},
	} {
		if r := c.reply(c.send(cl)); r.Err == nil {
			t.Errorf("%s: %+v", cl.Method, r)
		}
	}
}

func TestShutdown(t *testing.T) {
	h := newHarness(t, Options{State: t.TempDir() + "/daemon.json"})
	c := h.connect(true)
	c.event()
	c.call(playAll(songs(2), 0))
	h.ready()
	h.do(func() { h.fake.Pos = 30 })
	c.call(api.Call{Method: api.MethodShutdown})
	select {
	case <-h.svc.Done():
	case <-time.After(wait):
		t.Fatal("not done after Shutdown")
	}
	// Paused where it was, the engine closed; the peer reads the end.
	if !slices.Contains(h.fake.Calls, "pause") || !h.fake.Closed {
		t.Errorf("engine calls %q, closed %v", h.fake.Calls, h.fake.Closed)
	}
	for f := range c.frames {
		if f.Reply != nil {
			t.Errorf("reply %+v after Shutdown's", f)
		}
	}
	st, ok := loadState(h.svc.store.path)
	if !ok || st.Position != 30 || st.ResumePlaying {
		t.Errorf("saved %+v", st)
	}
	// Shutting down twice is harmless.
	h.svc.Shutdown()
}

// A connection after the Service stopped closes at once, unread.
func TestServeAfterStop(t *testing.T) {
	h := newHarness(t, Options{})
	h.svc.Shutdown()
	ours, theirs := net.Pipe()
	t.Cleanup(func() { _ = ours.Close() })
	served := make(chan struct{})
	go func() {
		h.svc.Serve(theirs)
		close(served)
	}()
	select {
	case <-served:
	case <-time.After(wait):
		t.Fatal("Serve runs a connection after the stop")
	}
	if _, err := ours.Read(make([]byte, 1)); err == nil {
		t.Error("the connection is still open")
	}
}
