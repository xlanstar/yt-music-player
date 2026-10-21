package remote

import (
	"slices"
	"strconv"
	"testing"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/ipc"
)

// applyCases are the events a front-end's thread takes most: a State,
// the snapshot of a long queue, and that of a reconnection, its songs
// ahead (daemon's carry). allocs is each one's budget
// (TestBudgetApplyEvents).
var applyCases = []struct {
	name   string
	events func() []*api.Event
	// relink: each op comes over a new link, as after a reconnection.
	relink bool
	allocs float64
}{
	// 0 allocs today.
	{"state", func() []*api.Event { return []*api.Event{stateEvent()} }, false, 0},
	// 0 allocs today: the songs' map holds them from the first.
	{"queue-5000", func() []*api.Event { return []*api.Event{queueEvent(5000)} }, false, 0},
	// 18 allocs today: the songs' map made anew for the queue.
	{"reconnect-5000", func() []*api.Event { return songsAhead(queueEvent(5000)) }, true, 21},
}

func stateEvent() *api.Event {
	return &api.Event{State: &api.State{
		Playing: true, Position: 81.5, Duration: 213, Volume: 0.7,
		Repeat: api.RepeatAll, Current: "song3", Index: 3,
	}}
}

func queueEvent(n int) *api.Event {
	ev := &api.Event{
		Queue: &api.Queue{IDs: make([]string, n)},
		Songs: make([]catalog.Song, n),
	}
	for i := range n {
		id := "song" + strconv.Itoa(i)
		ev.Queue.IDs[i] = id
		ev.Songs[i] = catalog.Song{ID: id, Title: "Song " + strconv.Itoa(i)}
	}
	return ev
}

// songsAhead is ev with its songs ahead in 16 events, as ytmd sends a
// snapshot.
func songsAhead(ev *api.Event) []*api.Event {
	var evs []*api.Event
	for part := range slices.Chunk(ev.Songs, (len(ev.Songs)+15)/16) {
		evs = append(evs, &api.Event{Songs: part})
	}
	return append(evs, &api.Event{Queue: ev.Queue})
}

// applyEvents receives evs and applies them, over a new link if relink.
func applyEvents(c *Client, l *link, evs []*api.Event, relink bool) {
	if relink {
		l.n++
	}
	for _, ev := range evs {
		c.receive(l, ev)
	}
	c.flush()
}

// applying is a Client fed events as its reader feeds them, flushed by
// hand rather than posted; its two batches, which swap, are made.
func applying() (*Client, *link) {
	c := NewClient(func(func()) {}, Handler{})
	l := &link{n: 1}
	for range 2 {
		c.receive(l, &api.Event{})
		c.flush()
	}
	return c, l
}

// BenchmarkApplyEvents receives an event and applies it on the
// front-end's thread.
func BenchmarkApplyEvents(b *testing.B) {
	for _, tc := range applyCases {
		b.Run(tc.name, func(b *testing.B) {
			c, l := applying()
			evs := tc.events()
			b.ReportAllocs()
			for b.Loop() {
				applyEvents(c, l, evs, tc.relink)
			}
			sinkState = c.Mirror().State()
		})
	}
}

var sinkState api.State

// BenchmarkCall is a Ping's way to a daemon over net.Pipe and back.
func BenchmarkCall(b *testing.B) {
	d := newDaemon("1.0.0")
	f := newFront()
	conn := ipc.NewConn(d.connect(b.Context()))
	h, err := greet(b.Context(), conn, hello(api.RoleTUI, "1.0.0", true))
	if err != nil {
		b.Fatal(err)
	}
	f.client.open(conn, compare(h, "1.0.0"), false)
	b.Cleanup(func() {
		f.client.Close()
		d.exit()
	})
	b.Run("ping", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if err := ping(b.Context(), f.client); err != nil {
				b.Fatal(err)
			}
		}
	})
}
