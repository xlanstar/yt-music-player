package daemon

import (
	"bytes"
	"encoding/gob"
	"errors"
	"io"
	"net"
	"os"
	"reflect"
	"slices"
	"strconv"
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
	if ev.Account != nil || ev.Likes != nil {
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

// A song queued without its details is told once, as an ID: details
// sent later stay with the daemon until it leaves the queue.
func TestQueuedUnknown(t *testing.T) {
	h := newHarness(t, Options{})
	c := h.connect(true)
	c.event()
	queue := songs(3)
	c.call(api.Call{Method: api.MethodPlayAll, IDs: &api.IDsArgs{IDs: ids(queue), Songs: queue[:1]}})
	ev := c.eventWith(func(ev api.Event) bool { return ev.Queue != nil })
	if !reflect.DeepEqual(ev.Songs, queue[:1]) {
		t.Errorf("new songs %+v", ev.Songs)
	}
	// Details of a queued song, the queue unchanged.
	c.call(api.Call{Method: api.MethodPlay, IDs: &api.IDsArgs{IDs: []string{"s1"}, Songs: queue[1:2]}})
	x := catalog.Song{ID: "x", Title: "X"}
	c.call(api.Call{Method: api.MethodEnqueue, IDs: &api.IDsArgs{IDs: []string{"s2", "x"}, Songs: []catalog.Song{queue[2], x}}})
	ev = c.eventWith(func(ev api.Event) bool { return ev.Queue != nil })
	if !reflect.DeepEqual(ev.Songs, []catalog.Song{x}) {
		t.Errorf("new songs %+v, want x alone", ev.Songs)
	}
	h.do(func() {
		if got := slices.Sorted(slices.Values(ids(h.svc.cat.Songs))); !slices.Equal(got, []string{"s0", "s1", "s2", "x"}) {
			t.Errorf("catalog %q", got)
		}
	})
	// Out of the queue and back, it goes again.
	c.call(api.Call{Method: api.MethodRemove, Index: &api.IndexArgs{Index: 2}})
	c.eventWith(func(ev api.Event) bool { return ev.Queue != nil })
	c.call(api.Call{Method: api.MethodEnqueue, IDs: &api.IDsArgs{IDs: []string{"s2"}, Songs: queue[2:]}})
	ev = c.eventWith(func(ev api.Event) bool { return ev.Queue != nil })
	if !reflect.DeepEqual(ev.Songs, queue[2:]) {
		t.Errorf("new songs %+v, want s2", ev.Songs)
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

// Replies too long for one frame come in parts of their lists, ahead of
// the reply, all with the request's ID.
func TestLongReplies(t *testing.T) {
	h := newHarness(t, Options{})
	c := h.connect(false)
	liked := catalogtest.Decoded(1000)
	id := c.send(api.Call{Method: api.MethodPlaylist, Ref: &api.RefArgs{ID: catalog.LikedID}})
	h.src.Next(t, nil, "playlist", catalog.LikedID).Reply <- catalogtest.Reply{
		Playlist: catalog.Playlist{ID: catalog.LikedID, Songs: ids(liked)}, Songs: liked,
	}
	r := c.reply(id)
	if r.Playlist == nil || !slices.Equal(r.Playlist.Playlist.Songs, ids(liked)) || !reflect.DeepEqual(r.Playlist.Songs, liked) {
		t.Errorf("playlist reply %+v", r.Playlist)
	}
	wantParts(t, c.ahead, 4)

	lib := catalog.Library{Songs: songs(300), Albums: []catalog.Album{{ID: "MPREb_1"}}}
	id = c.send(api.Call{Method: api.MethodLibrary})
	h.src.Next(t, nil, "library", "").Reply <- catalogtest.Reply{Library: lib}
	if r := c.reply(id); r.Library == nil || !reflect.DeepEqual(*r.Library, lib) {
		t.Errorf("library reply %+v", r.Library)
	}
	wantParts(t, c.ahead, 3)
}

// wantParts fails unless ahead are n parts of at most PartItems items.
func wantParts(t *testing.T, ahead []*api.Ahead, n int) {
	t.Helper()
	if len(ahead) != n {
		t.Errorf("%d parts ahead, want %d", len(ahead), n)
	}
	for i, a := range ahead {
		if items := len(a.Songs) + len(a.Albums) + len(a.Artists) + len(a.Playlists); items == 0 || items > api.PartItems {
			t.Errorf("part %d of %d items", i, items)
		}
	}
}

// failing is a connection that closed after its first frame.
type failing struct{ sent int }

func (f *failing) Send(api.Frame) error {
	f.sent++
	if f.sent > 1 {
		return ipc.ErrClosed
	}
	return nil
}

// A reply's parts stop at the first that fails to send: the connection
// closed, and the rest would fail too, each logged.
func TestReplyAheadStops(t *testing.T) {
	out := &failing{}
	c := &conn{out: out}
	r := &api.Reply{Playlist: &api.PlaylistReply{Songs: songs(5000)}}
	c.replyAhead(1, r.Split(), r)
	if out.sent != 2 {
		t.Errorf("%d sends, want 2", out.sent)
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

// A subscriber that stops reading keeps its connection while what a
// front-end asks at most at once waits for it: a queue's songs, the
// library, Liked music and a long playlist. Replies past the queue's
// bound close it.
func TestBackpressureReplies(t *testing.T) {
	h := newHarness(t, Options{})
	ours, theirs := net.Pipe()
	go h.svc.Serve(theirs)
	t.Cleanup(func() { _ = ours.Close() })
	const pid = 80
	enc := gob.NewEncoder(ours)
	send := func(f api.Frame) {
		t.Helper()
		if err := enc.Encode(&f); err != nil {
			t.Fatal(err)
		}
	}
	send(api.Frame{Hello: &api.Hello{Proto: api.Proto, Version: "1.2.3", Role: api.RoleDesktop, Subscribe: true, PID: pid}})
	// A byte read: the writer took the hello and snapshot of an empty
	// queue, and waits for the rest of them to be read.
	var first [1]byte
	if _, err := io.ReadFull(ours, first[:]); err != nil {
		t.Fatal(err)
	}
	long := songs(5000)
	h.connect(false).call(api.Call{Method: api.MethodEnqueue, IDs: &api.IDsArgs{IDs: ids(long), Songs: long}})
	lib := catalog.Library{Songs: long, Albums: make([]catalog.Album, 100), Artists: make([]catalog.Artist, 100), Playlists: make([]catalog.Playlist, 100)}
	playlist := func(id uint32, list string) {
		t.Helper()
		send(api.Frame{ID: id, Call: &api.Call{Method: api.MethodPlaylist, Ref: &api.RefArgs{ID: list}}})
		h.src.Next(t, nil, "playlist", list).Reply <- catalogtest.Reply{Playlist: catalog.Playlist{ID: list, Songs: ids(long)}, Songs: long}
	}
	send(api.Frame{ID: 1, Call: &api.Call{Method: api.MethodLibrary}})
	h.src.Next(t, nil, "library", "").Reply <- catalogtest.Reply{Library: lib}
	playlist(2, catalog.LikedID)
	playlist(3, "PLlong")
	// Each reply was sent with its parts, or the connection closed.
	stuck := func() *conn {
		for c := range h.svc.conns {
			if c.hello.PID == pid {
				return c
			}
		}
		return nil
	}
	h.until("answered", func() bool { c := stuck(); return c == nil || len(c.calls) == 0 })

	// All of it comes, then nothing closes.
	dec := gob.NewDecoder(io.MultiReader(bytes.NewReader(first[:]), ours))
	frames, ahead, replies := 0, map[uint32][]*api.Ahead{}, map[uint32]*api.Reply{}
	for len(replies) < 3 {
		var f api.Frame
		if err := dec.Decode(&f); err != nil {
			t.Fatalf("closed after %d frames: %v", frames, err)
		}
		frames++
		switch {
		case f.Ahead != nil:
			ahead[f.ID] = append(ahead[f.ID], f.Ahead)
		case f.Reply != nil:
			f.Reply.Join(ahead[f.ID])
			replies[f.ID] = f.Reply
		}
	}
	if r := replies[1].Library; r == nil || len(r.Songs)+len(r.Albums)+len(r.Artists)+len(r.Playlists) != 5300 {
		t.Errorf("library reply %+v", replies[1].Err)
	}
	for _, id := range []uint32{2, 3} {
		if r := replies[id].Playlist; r == nil || len(r.Songs) != len(long) {
			t.Errorf("playlist reply %d: %+v", id, replies[id].Err)
		}
	}
	t.Logf("%d frames waited", frames)

	// Replies past the queue's bound close it. The writer, taking the
	// queue whole, may hold a batch besides: 2*QueueLen frames at most.
	isClosed := func() bool {
		var gone bool
		h.do(func() { gone = stuck() == nil })
		return gone
	}
	const most = 2*ipc.QueueLen/17 + 1
	for id := uint32(4); !isClosed(); id++ {
		if id == 4+most {
			t.Fatalf("still open after %d replies", most)
		}
		list := "PL" + strconv.Itoa(int(id))
		if enc.Encode(&api.Frame{ID: id, Call: &api.Call{Method: api.MethodPlaylist, Ref: &api.RefArgs{ID: list}}}) != nil {
			break // it closed meanwhile
		}
		// The call reaches the source, unless the connection closed first.
		deadline := time.After(wait)
		for isAnswered := false; !isAnswered && !isClosed(); {
			select {
			case call := <-h.src.Calls:
				if call.Kind != "playlist" || call.ID != list {
					t.Fatalf("asked for %s %q, want playlist %q", call.Kind, call.ID, list)
				}
				call.Reply <- catalogtest.Reply{Playlist: catalog.Playlist{ID: list, Songs: ids(long)}, Songs: long}
				isAnswered = true
			case <-time.After(10 * time.Millisecond):
			case <-deadline:
				t.Fatalf("no request for playlist %q", list)
			}
		}
	}
}

// TestSubscriberRefetchesFailedAccount: who is signed in, whose fetch
// failed at start (offline at login), is fetched again for a front-end
// that subscribes later, once.
func TestSubscriberRefetchesFailedAccount(t *testing.T) {
	h := newHarness(t, Options{})
	h.svc.Start()
	fetch := h.src.Next(t, nil, "account", "")
	// Nothing failed yet: a subscriber asks nothing.
	first := h.connect(true)
	first.event()
	h.src.None(t, nil, 30*time.Millisecond)
	fetch.Reply <- catalogtest.Reply{Err: errors.New("offline")}
	first.eventWith(func(ev api.Event) bool { return ev.Failed != nil })

	c := h.connect(true)
	if snap := c.event(); snap.Failed == nil {
		t.Fatalf("snapshot %+v, want the failure", snap)
	}
	fetch = h.src.Next(t, nil, "account", "")
	// Another meanwhile leaves the fetch in flight.
	h.connect(true).event()
	h.src.None(t, nil, 30*time.Millisecond)
	ada := catalog.Account{Name: "Ada"}
	fetch.Reply <- catalogtest.Reply{Account: ada}
	if a := c.eventWith(func(ev api.Event) bool { return ev.Account != nil }).Account; a.Account != ada || !a.SignedIn {
		t.Errorf("account %+v", a)
	}
	h.connect(true).event()
	h.src.None(t, nil, 30*time.Millisecond)
}

func TestAccount(t *testing.T) {
	h := newHarness(t, Options{})
	c := h.connect(true)
	c.event()
	h.svc.Start()
	// Who is signed in, and nothing more: front-ends fetch the library and
	// likes.
	h.src.Next(t, nil, "account", "").Reply <- catalogtest.Reply{Err: errors.New("offline")}
	h.src.None(t, nil, 30*time.Millisecond)
	if f := c.event().Failed; f == nil || !errors.Is(f.Err.Err(), api.ErrInternal) {
		t.Errorf("failed %+v", f)
	}
	// A new subscriber hears of the failure in its snapshot, and has who
	// is signed in fetched again (TestSubscriberRefetchesFailedAccount).
	if snap := h.connect(true).event(); snap.Account != nil || snap.Failed == nil {
		t.Errorf("snapshot %+v", snap)
	}
	h.src.Next(t, nil, "account", "").Reply <- catalogtest.Reply{Err: errors.New("offline")}
	if f := c.event().Failed; f == nil {
		t.Errorf("failed %+v", f)
	}

	// Refresh, as a page's Retry.
	c.call(api.Call{Method: api.MethodRefresh})
	ada := catalog.Account{Name: "Ada"}
	h.src.Next(t, nil, "account", "").Reply <- catalogtest.Reply{Account: ada}
	if a := c.eventWith(func(ev api.Event) bool { return ev.Account != nil }).Account; a.Account != ada || !a.SignedIn || a.Switched {
		t.Errorf("account %+v", a)
	}

	// A like's reply comes before its change.
	id := c.send(api.Call{Method: api.MethodLike, Like: &api.LikeArgs{ID: "s9", Liked: true}})
	h.src.Next(t, nil, "like", "+s9").Reply <- catalogtest.Reply{}
	if r := c.reply(id); r.Err != nil || len(c.events) > 0 {
		t.Fatalf("like reply %+v after events %+v", r, c.events)
	}
	if l := c.event().Likes; l == nil || !slices.Equal(l.Added, []string{"s9"}) || l.Songs != nil {
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

	// A new subscriber learns who is signed in, and keeps no likes.
	late := h.connect(true)
	if snap := late.event(); snap.Account == nil || snap.Account.Account != ada || snap.Likes != nil || snap.Failed != nil {
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

// A front-end's fetch answered ErrSignedOut signs ytmd out, as its own
// do, unless the credentials changed since it was asked.
func TestFetchSignedOut(t *testing.T) {
	h := newHarness(t, Options{})
	c := h.connect(true)
	c.event()
	h.signedIn()
	c.eventWith(func(ev api.Event) bool { return ev.Account != nil })

	stale := c.send(api.Call{Method: api.MethodPlaylist, Ref: &api.RefArgs{ID: catalog.LikedID}})
	staleCall := h.src.Next(t, nil, "playlist", catalog.LikedID)
	id := c.send(signIn("SID=grace"))
	h.src.Next(t, nil, "signin", "SID=grace").Reply <- catalogtest.Reply{Account: catalog.Account{Name: "Grace"}}
	c.reply(id)
	if a := c.eventWith(func(ev api.Event) bool { return ev.Account != nil }).Account; !a.Switched {
		t.Fatalf("account %+v", a)
	}
	staleCall.Reply <- catalogtest.Reply{Err: catalog.ErrSignedOut}
	if r := c.reply(stale); !errors.Is(r.Err.Err(), catalog.ErrSignedOut) {
		t.Errorf("reply %+v", r)
	}
	c.ping()
	for _, ev := range c.events {
		if ev.Account != nil {
			t.Errorf("the previous credentials' answer told %+v", ev.Account)
		}
	}
	c.events = nil

	id = c.send(api.Call{Method: api.MethodLibrary})
	h.src.Next(t, nil, "library", "").Reply <- catalogtest.Reply{Err: catalog.ErrSignedOut}
	if r := c.reply(id); !errors.Is(r.Err.Err(), catalog.ErrSignedOut) || len(c.events) > 0 {
		t.Fatalf("library reply %+v after %+v", r, c.events)
	}
	if a := c.event().Account; a == nil || a.SignedIn {
		t.Errorf("account event %+v, want signed out", a)
	}
	h.until("signed out", func() bool { return !h.svc.status().SignedIn })
}

// Songs too many for one frame go ahead of the event naming them, in
// events of songs alone, which every front-end keeps.
func TestLongLists(t *testing.T) {
	h := newHarness(t, Options{})
	c := h.connect(true)
	c.event()
	queue := songs(1000)
	c.call(playAll(queue, 0))
	ev, ahead := c.songsAhead(func(ev api.Event) bool { return ev.Queue != nil })
	if !slices.Equal(ev.Queue.IDs, ids(queue)) || ev.Songs != nil || !reflect.DeepEqual(ahead, queue) {
		t.Errorf("queue %d IDs, %d songs, %d ahead", len(ev.Queue.IDs), len(ev.Songs), len(ahead))
	}

	late := h.connect(true)
	snap, queued := late.songsAhead(func(ev api.Event) bool { return ev.Queue != nil })
	byID := func(a, b catalog.Song) int { return strings.Compare(a.ID, b.ID) }
	slices.SortFunc(queued, byID)
	slices.SortFunc(queue, byID)
	if !slices.Equal(snap.Queue.IDs, ids(songs(1000))) || snap.Songs != nil || !reflect.DeepEqual(queued, queue) {
		t.Errorf("snapshot's queue %d IDs, %d songs, %d ahead", len(snap.Queue.IDs), len(snap.Songs), len(queued))
	}
}

// A front-end's call too long for one frame comes in parts of its songs,
// ahead of it with its ID, and dispatches whole.
func TestLongCalls(t *testing.T) {
	h := newHarness(t, Options{})
	sub := h.connect(true)
	sub.event()
	c := h.connect(false)
	// Another call's parts in between change nothing.
	other := playAll(songs(api.PartItems+1), 0)
	for _, a := range other.Split() {
		if err := c.c.Send(api.Frame{ID: 99, Ahead: a}); err != nil {
			t.Fatal(err)
		}
	}
	queue := catalogtest.Decoded(5000)
	if r := c.reply(c.sendInParts(playAll(queue, 2))); r.Err != nil {
		t.Fatal(r.Err.Err())
	}
	ev, ahead := sub.songsAhead(func(ev api.Event) bool { return ev.Queue != nil })
	if !slices.Equal(ev.Queue.IDs, ids(queue)) || !reflect.DeepEqual(ahead, queue) {
		t.Errorf("queue %d IDs, %d songs, want %d", len(ev.Queue.IDs), len(ahead), len(queue))
	}
	if ev.State.Index != 2 {
		t.Errorf("playing %d, want 2", ev.State.Index)
	}
}

// Parts whose calls never come close the connection once more wait than
// front-ends send at once.
func TestPartsWithoutCalls(t *testing.T) {
	h := newHarness(t, Options{})
	c := h.connect(false)
	part := &api.Ahead{Songs: songs(1)}
	for id := range uint32(maxAhead) {
		if err := c.c.Send(api.Frame{ID: 100 + id, Ahead: part}); err != nil {
			t.Fatal(err)
		}
		if id%api.MaxParts == 0 {
			c.ping() // within the test's own queue
		}
	}
	c.ping()
	if err := c.c.Send(api.Frame{ID: 1, Ahead: part}); err != nil {
		t.Fatal(err)
	}
	c.closed()
}

// carry sends an event's long list of songs whole ahead, in at most
// api.MaxParts frames.
func TestCarry(t *testing.T) {
	for _, tc := range []struct{ n, frames, kept int }{
		{0, 0, 0},
		{api.PartItems, 0, api.PartItems},
		{api.PartItems + 1, 2, 0},
		{api.PartItems * api.MaxParts, api.MaxParts, 0},
		{100_000, api.MaxParts, 0},
	} {
		list := songs(tc.n)
		ev := api.Event{Songs: list}
		ahead := carry(nil, &ev)
		var queued []catalog.Song
		for _, f := range ahead {
			queued = append(queued, f.Event.Songs...)
		}
		queued = append(queued, ev.Songs...)
		if len(ahead) != tc.frames || len(ev.Songs) != tc.kept || !slices.Equal(queued, list) {
			t.Errorf("%d songs: %d frames, %d kept, %d sent; want %d frames, %d kept",
				tc.n, len(ahead), len(ev.Songs), len(queued), tc.frames, tc.kept)
		}
	}
}

// Started, ytmd fetches who is signed in, for `ytm status`, and nothing
// more: neither subscribers nor a sign-in fetch anything.
func TestFetchesOnlyAccount(t *testing.T) {
	h := newHarness(t, Options{})
	cli := h.connect(false)
	h.signedIn()
	h.until("signed in", func() bool { return h.svc.status().SignedIn })
	if st := cli.call(api.Call{Method: api.MethodStatus}).Status; st.Account.Name != "Ada" {
		t.Errorf("status %+v", st)
	}
	if a := h.connect(true).event().Account; a == nil || a.Account.Name != "Ada" {
		t.Errorf("account %+v", a)
	}
	id := cli.send(signIn("SID=1"))
	h.src.Next(t, nil, "signin", "SID=1").Reply <- catalogtest.Reply{Account: catalog.Account{Name: "Grace"}}
	if r := cli.reply(id); r.Err != nil {
		t.Fatal(r.Err.Err())
	}
	h.src.None(t, nil, 30*time.Millisecond)
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
