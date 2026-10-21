package daemon

import (
	"context"
	"net"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/audio"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/catalog/catalogtest"
	"yt-music-player/internal/ipc"
	"yt-music-player/internal/stream"

	"github.com/rs/zerolog"
)

func TestMain(m *testing.M) {
	// Only main sets the level: tests would otherwise print every failure
	// they provoke.
	zerolog.SetGlobalLevel(zerolog.Disabled)
	runTests(m)
}

// wait bounds every wait for the Service.
const wait = 5 * time.Second

// instantResolver answers at once with a stream named after the song.
type instantResolver struct{}

func (instantResolver) Resolve(_ context.Context, id string) (stream.Stream, error) {
	return stream.Stream{URL: "https://audio/" + id}, nil
}

func (instantResolver) Forget(string) {}

// clock is a Service's clock that the test moves.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock { return &clock{now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// harness is a Service over catalogtest, a fake engine that the loop owns
// (touch it through do), and a clock the test moves.
type harness struct {
	t     *testing.T
	svc   *Service
	src   catalogtest.Accounts
	fake  *audio.Fake
	clock *clock
}

// newHarness makes a Service with opts over the harness's source, engine
// and clock; the test may set opts' State, Seed and SaveDelay.
func newHarness(t *testing.T, opts Options) *harness {
	t.Helper()
	h := &harness{t: t, src: catalogtest.Accounts{Source: catalogtest.New(t)}, clock: newClock()}
	var emit func(audio.Event)
	h.fake = audio.NewFake(func(ev audio.Event) { emit(ev) })
	opts.Source = h.src
	opts.Resolver = instantResolver{}
	opts.NewEngine = func(events func(audio.Event)) (audio.Engine, error) {
		emit = events
		return h.fake, nil
	}
	opts.Now = h.clock.Now
	h.svc = New(opts)
	t.Cleanup(h.close)
	return h
}

func (h *harness) close() {
	done := make(chan struct{})
	go func() {
		h.svc.Shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(wait):
		h.t.Error("Shutdown did not return")
	}
}

// do runs fn on the loop and waits for it.
func (h *harness) do(fn func()) {
	h.t.Helper()
	ran := make(chan struct{})
	h.svc.post(func() {
		fn()
		close(ran)
	})
	select {
	case <-ran:
	case <-time.After(wait):
		h.t.Fatal("the loop did not run")
	}
}

// songs are n songs named after i, 200 s each.
func songs(n int) []catalog.Song {
	out := make([]catalog.Song, n)
	for i := range out {
		id := "s" + strconv.Itoa(i)
		out[i] = catalog.Song{ID: id, Title: "Song " + id, Artist: "Artist", Seconds: 200}
	}
	return out
}

func ids(songs []catalog.Song) []string {
	out := make([]string, len(songs))
	for i, s := range songs {
		out[i] = s.ID
	}
	return out
}

// client is a front-end connected to the harness's Service over a pipe.
type client struct {
	t      *testing.T
	c      *ipc.Conn
	hello  api.Hello // the daemon's
	nextID uint32
	frames chan api.Frame
	// events are those received while waiting for a reply.
	events []api.Event
	// ahead are the parts of the last reply, which came before it.
	ahead []*api.Ahead
}

// connect greets the Service as a front-end of role and Proto; a
// subscriber gets its snapshot in events.
func (h *harness) connect(subscribe bool) *client {
	h.t.Helper()
	return h.connectAs(api.Hello{Proto: api.Proto, Version: "1.2.3", Role: api.RoleDesktop, Subscribe: subscribe, PID: 77})
}

func (h *harness) connectAs(hello api.Hello) *client {
	h.t.Helper()
	ours, theirs := net.Pipe()
	go h.svc.Serve(theirs)
	return newClient(h.t, ours, hello)
}

// newClient greets the daemon at the other end of nc.
func newClient(t *testing.T, nc net.Conn, hello api.Hello) *client {
	t.Helper()
	c := &client{t: t, c: ipc.NewConn(nc), frames: make(chan api.Frame, 1024)}
	t.Cleanup(func() { _ = c.c.Close() })
	go func() {
		defer close(c.frames)
		for {
			var f api.Frame
			if c.c.Receive(&f) != nil {
				return
			}
			c.frames <- f
		}
	}()
	if err := c.c.Send(api.Frame{Hello: &hello}); err != nil {
		t.Fatal(err)
	}
	f := c.next()
	if f.Hello == nil {
		t.Fatalf("first frame %+v, want the daemon's hello", f)
	}
	c.hello = *f.Hello
	return c
}

// closed waits until the daemon closed the connection, failing on a
// reply meanwhile.
func (c *client) closed() {
	c.t.Helper()
	timeout := time.After(wait)
	for {
		select {
		case f, ok := <-c.frames:
			if !ok {
				return
			}
			if f.Reply != nil {
				c.t.Errorf("reply %+v, want none", f)
			}
		case <-timeout:
			c.t.Fatal("still connected")
		}
	}
}

// next returns the next frame.
func (c *client) next() api.Frame {
	c.t.Helper()
	select {
	case f, ok := <-c.frames:
		if !ok {
			c.t.Fatal("disconnected")
		}
		return f
	case <-time.After(wait):
		c.t.Fatal("no frame came")
	}
	return api.Frame{}
}

// send sends a call and returns its ID.
func (c *client) send(cl api.Call) uint32 {
	c.t.Helper()
	c.nextID++
	if err := c.c.Send(api.Frame{ID: c.nextID, Call: &cl}); err != nil {
		c.t.Fatal(err)
	}
	return c.nextID
}

// reply waits for the reply to id, its parts joined and kept in ahead,
// keeping the events before it.
func (c *client) reply(id uint32) api.Reply {
	c.t.Helper()
	c.ahead = nil
	for {
		f := c.next()
		switch {
		case f.Event != nil:
			c.events = append(c.events, *f.Event)
		case f.Reply != nil && f.ID == id && f.Reply.Ahead != nil:
			c.ahead = append(c.ahead, f.Reply.Ahead)
		case f.Reply != nil && f.ID == id:
			f.Reply.Join(c.ahead)
			return *f.Reply
		case f.Reply != nil:
			c.t.Fatalf("reply to %d while waiting for %d's", f.ID, id)
		}
	}
}

// call sends cl and waits for its reply, which must not fail.
func (c *client) call(cl api.Call) api.Reply {
	c.t.Helper()
	r := c.reply(c.send(cl))
	if r.Err != nil {
		c.t.Fatalf("%s failed: %v", cl.Method, r.Err.Err())
	}
	return r
}

// event returns the next event, kept or to come; frames other than events
// fail the test.
func (c *client) event() api.Event {
	c.t.Helper()
	if len(c.events) > 0 {
		ev := c.events[0]
		c.events = c.events[1:]
		return ev
	}
	f := c.next()
	if f.Event == nil {
		c.t.Fatalf("frame %+v, want an event", f)
	}
	return *f.Event
}

// eventWith returns the next event that has, per has, what the test waits
// for, dropping those before it.
func (c *client) eventWith(has func(api.Event) bool) api.Event {
	c.t.Helper()
	for {
		if ev := c.event(); has(ev) {
			return ev
		}
	}
}

// songsAhead returns the next event last accepts, with the queue's songs
// that came ahead of it in events of their own. Other events are dropped.
func (c *client) songsAhead(last func(api.Event) bool) (ev api.Event, queued []catalog.Song) {
	c.t.Helper()
	for {
		ev = c.event()
		switch {
		case last(ev):
			return ev, queued
		case reflect.DeepEqual(ev, api.Event{Songs: ev.Songs}):
			if len(ev.Songs) == 0 || len(ev.Songs) > songsPerFrame {
				c.t.Errorf("an event of %d songs", len(ev.Songs))
			}
			queued = append(queued, ev.Songs...)
		}
	}
}

// state returns the next State event, dropping other events.
func (c *client) state() api.State {
	c.t.Helper()
	return *c.eventWith(func(ev api.Event) bool { return ev.State != nil }).State
}

// none fails the test if a frame comes within d.
func (c *client) none(d time.Duration) {
	c.t.Helper()
	if len(c.events) > 0 {
		c.t.Fatalf("events %+v, want none", c.events)
	}
	select {
	case f, ok := <-c.frames:
		if ok {
			c.t.Fatalf("frame %+v, want none", f)
		}
	case <-time.After(d):
	}
}

// ping is a round trip: every event caused before it has come once it
// returns (in events).
func (c *client) ping() {
	c.t.Helper()
	c.call(api.Call{Method: api.MethodPing})
}

func playAll(songs []catalog.Song, start int) api.Call {
	return api.Call{Method: api.MethodPlayAll, IDs: &api.IDsArgs{IDs: ids(songs), Songs: songs, Start: start}}
}

// loaded waits until the engine has a load: the stream resolves off the
// loop.
func (h *harness) loaded() {
	h.t.Helper()
	h.until("loaded", func() bool { return h.fake.Loads > 0 })
}

// signedIn starts the Service and answers its fetch of who is signed in
// as Ada's.
func (h *harness) signedIn() {
	h.t.Helper()
	h.svc.Start()
	h.src.Next(h.t, nil, "account", "").Reply <- catalogtest.Reply{Account: catalog.Account{Name: "Ada"}}
}

// until waits until cond, run on the loop, holds: what happens off the
// loop comes back through it.
func (h *harness) until(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(wait)
	for {
		var ok bool
		h.do(func() { ok = cond() })
		if ok {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("never %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// ready has the fake engine report the load ready, 200 s long.
func (h *harness) ready() {
	h.t.Helper()
	h.loaded()
	h.do(func() {
		h.fake.Dur = 200
		h.fake.Emit(audio.StreamReady)
	})
}

// calls returns the engine calls since the last.
func (h *harness) calls() []string {
	h.t.Helper()
	var calls []string
	h.do(func() { calls = h.fake.TakeCalls() })
	return calls
}

func hasState(ev api.Event) bool { return ev.State != nil }
