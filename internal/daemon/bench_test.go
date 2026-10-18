package daemon

import (
	"path/filepath"
	"strconv"
	"testing"
	"yt-music-player/internal/account"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
)

// bigQueue is a large library played in full.
const bigQueue = 2000

// sink is a subscriber that drops what it is sent: no socket, so only the
// daemon's own cost is measured.
type sink struct{}

func (sink) Send(api.Frame) error { return nil }

// benchService is a Service whose loop does not run: the benchmark runs
// its work instead, with one subscriber. Without a Resolver nothing plays,
// so nothing comes back through the loop.
func benchService(tb testing.TB, queue int) (*Service, *conn) {
	tb.Helper()
	seed := make([]catalog.Song, queue)
	for i := range seed {
		id := "song-" + strconv.Itoa(i)
		seed[i] = catalog.Song{ID: id, Title: id, Artist: "Artist", Seconds: 200}
	}
	s := newService(Options{Source: catalog.NewPlaceholderSource(0), Seed: seed})
	c := &conn{out: sink{}, compatible: true, subscribed: true, calls: map[uint32]func(){}}
	s.conns[c] = struct{}{}
	return s, c
}

// request is a request's way through the loop: its dispatch, then what
// the batch changed sent.
func request(s *Service, c *conn, cl *api.Call) {
	s.dispatch(c, 1, cl)
	s.settle()
}

var (
	pingCall   = &api.Call{Method: api.MethodPing}
	toggleCall = &api.Call{Method: api.MethodToggle}
	seekCalls  = [2]*api.Call{
		{Method: api.MethodSeek, Seek: &api.SeekArgs{Seconds: 30}},
		{Method: api.MethodSeek, Seek: &api.SeekArgs{Seconds: 90}},
	}
	enqueueCall = &api.Call{Method: api.MethodEnqueue, IDs: &api.IDsArgs{
		IDs: []string{"new"}, Songs: []catalog.Song{{ID: "new", Title: "New"}}, Next: true,
	}}
	removeCall = &api.Call{Method: api.MethodRemove, Index: &api.IndexArgs{Index: 1}}
)

// dispatchCases are requests, one op each; their budgets live here.
var dispatchCases = []struct {
	name  string
	queue int
	op    func(s *Service, c *conn, i int)
	limit float64
}{
	// 1 alloc today: the reply.
	{"ping", 3, func(s *Service, c *conn, _ int) { request(s, c, pingCall) }, 1},
	// 3 allocs today: the reply, the event and its State.
	{"toggle", 3, func(s *Service, c *conn, _ int) { request(s, c, toggleCall) }, 4},
	// 3 allocs today, as toggle's.
	{"seek", 3, func(s *Service, c *conn, i int) { request(s, c, seekCalls[i%2]) }, 4},
	// A song in and out of a long queue: each a reply, the event and its
	// State, the queue's IDs, the new songs, the catalog kept. 13 allocs
	// today.
	{"enqueue-remove/" + strconv.Itoa(bigQueue), bigQueue, func(s *Service, c *conn, _ int) {
		request(s, c, enqueueCall)
		request(s, c, removeCall)
	}, 15},
}

func BenchmarkDispatch(b *testing.B) {
	for _, tc := range dispatchCases {
		b.Run(tc.name, func(b *testing.B) {
			s, c := benchService(b, tc.queue)
			i := 0
			b.ReportAllocs()
			for b.Loop() {
				tc.op(s, c, i)
				i++
			}
		})
	}
}

// likesCases are a like that went through, told to a subscriber.
var likesCases = []struct {
	name   string
	change account.Change
	limit  float64
}{
	// 2 allocs today: the event and its Likes.
	{"added", account.Change{Likes: &account.Likes{Added: []string{"song-1"}}}, 2},
	{"removed", account.Change{Likes: &account.Likes{Removed: []string{"song-1"}}}, 2},
}

func BenchmarkLikesDiff(b *testing.B) {
	for _, tc := range likesCases {
		b.Run(tc.name, func(b *testing.B) {
			s, _ := benchService(b, 3)
			b.ReportAllocs()
			for b.Loop() {
				s.accountChanged(tc.change)
			}
		})
	}
}

// BenchmarkSnapshot is what a subscriber connecting costs the loop.
func BenchmarkSnapshot(b *testing.B) {
	s, _ := benchService(b, bigQueue)
	b.ReportAllocs()
	for b.Loop() {
		sinkFrames = s.snapshot()
	}
}

// BenchmarkWrite saves a large queue's daemon.json, as the store's
// goroutine does after a pause or a new song.
func BenchmarkWrite(b *testing.B) {
	s, _ := benchService(b, bigQueue)
	st := s.saved()
	w := newStore(filepath.Join(b.TempDir(), "daemon.json"), 0)
	b.ReportAllocs()
	for b.Loop() {
		w.write(st)
	}
}

var sinkFrames []api.Frame
