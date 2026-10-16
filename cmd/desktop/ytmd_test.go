package main

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/audio"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/daemon"
	"yt-music-player/internal/remote"
	"yt-music-player/internal/stream"
	"yt-music-player/internal/version"

	"github.com/egoist/mygo/ui"
)

// Tests run the app over ytmd in this process: a daemon.Service, with no
// saved state, connected to the app's client over net.Pipe. It plays the
// placeholder's queue on audio.Fake when a test gives it a Resolver, and
// acts as a mock otherwise. Its source is tracked, so that answer knows
// when the daemon took a reply.

// wait bounds every wait for the daemon, the app's posts or a request.
const wait = 5 * time.Second

// posts stand in for Window.Update, as tests have no window: what the app
// posts queues in app, what its remote Client posts in client, and the
// test runs them on its goroutine, the Tester's UI thread. Every build
// runs the Client's first, as a window runs Update's funcs before a
// frame; the app's wait for the test, which may hold them.
type posts struct {
	app, client chan func()
	ytmd        *testYtmd
}

// testYtmd is an app's in-process ytmd.
type testYtmd struct {
	svc *daemon.Service
	src *tracked
	// engine plays, nil without a Resolver.
	engine *testEngine
	q      posts
	// a is the app connected, nil once closed: a dropped screen must
	// leave nothing that reaches it.
	a *app
}

var (
	ytmdsMu sync.Mutex
	ytmds   = map[*app]*testYtmd{}
)

// ytmdOf returns a's in-process ytmd.
func ytmdOf(a *app) *testYtmd {
	ytmdsMu.Lock()
	defer ytmdsMu.Unlock()
	y := ytmds[a]
	if y == nil {
		panic("an app without its test ytmd")
	}
	return y
}

// ytmdOptions are what a test's ytmd plays with; the zero value plays
// nothing.
type ytmdOptions struct {
	resolver stream.Resolver
	// signedOut answers the account's fetches at once: no one is signed
	// in, so only pages ask src for music.
	signedOut bool
	// isDetached leaves the app's posts to the test to attach.
	isDetached bool
}

// startApp is an app over an in-process ytmd serving src, connected and
// greeted with the daemon's snapshot: the placeholder's queue, whatever
// src is. The daemon stops with the test.
func startApp(tb testing.TB, src catalog.Source, opts ytmdOptions) (*app, posts) {
	tb.Helper()
	if opts.signedOut {
		src = signedOutSource{src}
	}
	y := &testYtmd{src: newTracked(src)}
	dopts := daemon.Options{Source: y.src.source(), Seed: catalog.Placeholder().Songs}
	if opts.resolver != nil {
		y.engine = &testEngine{}
		dopts.Resolver = opts.resolver
		dopts.NewEngine = y.engine.start
	}
	svc, err := daemon.New(dopts)
	if err != nil {
		tb.Fatal(err)
	}
	svc.Start()
	y.svc = svc
	// Large: the Client must never wait on its post.
	y.q = posts{app: make(chan func(), 64), client: make(chan func(), 1024), ytmd: y}
	a := newDemoApp(func(fn func()) { y.q.client <- fn })
	if !opts.isDetached {
		a.updates.attach(func(fn func()) { y.q.app <- fn })
	}
	y.a = a
	ytmdsMu.Lock()
	ytmds[a] = y
	ytmdsMu.Unlock()
	a.connect(ytmd{dialer: pipeDialer{svc}, spawn: errNoSpawn, version: version.Dev})
	tb.Cleanup(y.close)
	y.until(tb, "connected", func() bool {
		return a.daemon.link.Health == remote.Connected && a.mirror.StateGen() > 0
	})
	syncDaemon(tb, y.q)
	return a, y.q
}

func errNoSpawn() error { return errors.New("tests start no ytmd") }

// close disconnects the app and stops its ytmd.
func (y *testYtmd) close() {
	y.disconnect()
	y.svc.Shutdown()
}

// disconnect disconnects the app, once, and forgets it.
func (y *testYtmd) disconnect() {
	if y.a == nil {
		return
	}
	ytmdsMu.Lock()
	delete(ytmds, y.a)
	ytmdsMu.Unlock()
	y.a.disconnect()
	y.a = nil
}

// pipeDialer connects to an in-process Service over net.Pipe.
type pipeDialer struct{ svc *daemon.Service }

func (d pipeDialer) Dial(context.Context) (net.Conn, error) {
	ours, theirs := net.Pipe()
	go d.svc.Serve(theirs) //nolint:contextcheck // the Service's end outlives the dial, as a socket's does
	return ours, nil
}

func (d pipeDialer) Wait(ctx context.Context) (net.Conn, error)        { return d.Dial(ctx) }
func (d pipeDialer) DialOrSpawn(ctx context.Context) (net.Conn, error) { return d.Dial(ctx) }

// runClient runs what a's Client posted, as a window does before a frame.
func (y *testYtmd) runClient() {
	for {
		select {
		case fn := <-y.q.client:
			fn()
		default:
			return
		}
	}
}

// until runs the Client's posts until cond holds, checking it after each
// and every millisecond, for what changes elsewhere.
func (y *testYtmd) until(tb testing.TB, what string, cond func() bool) {
	tb.Helper()
	timeout := time.After(wait)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !cond() {
		select {
		case fn := <-y.q.client:
			fn()
		case <-tick.C:
		case <-timeout:
			tb.Fatalf("never %s", what)
		}
	}
}

// syncDaemon is a round trip to ytmd: every event caused before it, by a
// control or by the daemon's own loop, has been applied once it returns.
// The first Ping may share the daemon's batch with what came before, whose
// events follow the batch's replies; the second, sent after the first's
// reply, comes in a later batch.
func syncDaemon(tb testing.TB, q posts) {
	tb.Helper()
	y := q.ytmd
	for range 2 {
		isDone := false
		y.a.client.Go(context.Background(), api.Call{Method: api.MethodPing}, func(_ *api.Reply, err error) {
			if err != nil {
				tb.Errorf("ping: %v", err)
			}
			isDone = true
		})
		y.until(tb, "pinged", func() bool { return isDone })
	}
}

// tracked is a Source that counts the requests it answered, by kind and
// ID as catalogtest names them.
type tracked struct {
	src      catalog.Source
	mu       sync.Mutex
	returned map[string]int
}

func newTracked(src catalog.Source) *tracked {
	return &tracked{src: src, returned: map[string]int{}}
}

// source returns t as the daemon's source: catalog.Accounts when t's
// source signs in.
func (t *tracked) source() catalog.Source {
	if _, ok := t.src.(catalog.Accounts); ok {
		return trackedAccounts{t}
	}
	return t
}

func (t *tracked) answered(kind, id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.returned[kind+" "+id]++
}

// count returns how many requests of kind and id were answered.
func (t *tracked) count(kind, id string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.returned[kind+" "+id]
}

// The deferred count runs before the caller, the daemon, gets the
// result, which it posts to its loop at once.

func (t *tracked) Search(ctx context.Context, query string) (catalog.Results, error) {
	defer t.answered("search", query)
	return t.src.Search(ctx, query)
}

func (t *tracked) Album(ctx context.Context, id string) (catalog.Album, []catalog.Song, error) {
	defer t.answered("album", id)
	return t.src.Album(ctx, id)
}

func (t *tracked) Playlist(ctx context.Context, id string) (catalog.Playlist, []catalog.Song, error) {
	defer t.answered("playlist", id)
	return t.src.Playlist(ctx, id)
}

func (t *tracked) Home(ctx context.Context) (catalog.Home, error) {
	defer t.answered("home", "")
	return t.src.Home(ctx)
}

func (t *tracked) Explore(ctx context.Context) (catalog.Explore, error) {
	defer t.answered("explore", "")
	return t.src.Explore(ctx)
}

func (t *tracked) Account(ctx context.Context) (catalog.Account, error) {
	defer t.answered("account", "")
	return t.src.Account(ctx)
}

func (t *tracked) Library(ctx context.Context) (catalog.Library, error) {
	defer t.answered("library", "")
	return t.src.Library(ctx)
}

func (t *tracked) Like(ctx context.Context, id string, liked bool) error {
	sign := "-"
	if liked {
		sign = "+"
	}
	defer t.answered("like", sign+id)
	return t.src.Like(ctx, id, liked)
}

type trackedAccounts struct{ *tracked }

func (t trackedAccounts) SignIn(ctx context.Context, credentials string) (catalog.Account, error) {
	defer t.answered("signin", credentials)
	return t.src.(catalog.Accounts).SignIn(ctx, credentials)
}

func (t trackedAccounts) SignOut() error {
	defer t.answered("signout", "")
	return t.src.(catalog.Accounts).SignOut()
}

// signedOutSource has no one signed in: it answers the account's fetches
// at once, and passes the rest on.
type signedOutSource struct{ catalog.Source }

func (signedOutSource) Account(context.Context) (catalog.Account, error) {
	return catalog.Account{}, catalog.ErrSignedOut
}

func (signedOutSource) Library(context.Context) (catalog.Library, error) {
	return catalog.Library{}, catalog.ErrSignedOut
}

func (s signedOutSource) Playlist(ctx context.Context, id string) (catalog.Playlist, []catalog.Song, error) {
	if id == catalog.LikedID {
		return catalog.Playlist{}, nil, catalog.ErrSignedOut
	}
	return s.Source.Playlist(ctx, id)
}

// testEngine is the daemon's engine, an audio.Fake that the daemon's loop
// and the test share under a lock.
type testEngine struct {
	mu     sync.Mutex
	fake   *audio.Fake
	events func(audio.Event)
}

func (e *testEngine) start(events func(audio.Event)) (audio.Engine, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = events
	e.fake = audio.NewFake(events)
	return e, nil
}

// do runs fn on the fake, once the daemon made it.
func (e *testEngine) do(fn func(f *audio.Fake)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.fake != nil {
		fn(e.fake)
	}
}

func (e *testEngine) Load(s audio.Media, at float64) { e.do(func(f *audio.Fake) { f.Load(s, at) }) }
func (e *testEngine) Play()                          { e.do(func(f *audio.Fake) { f.Play() }) }
func (e *testEngine) Pause()                         { e.do(func(f *audio.Fake) { f.Pause() }) }
func (e *testEngine) Seek(sec float64)               { e.do(func(f *audio.Fake) { f.Seek(sec) }) }
func (e *testEngine) SetVolume(v float64)            { e.do(func(f *audio.Fake) { f.SetVolume(v) }) }

func (e *testEngine) SetInfo(info audio.Info) { e.do(func(f *audio.Fake) { f.SetInfo(info) }) }
func (e *testEngine) Close()                  { e.do(func(f *audio.Fake) { f.Close() }) }

func (e *testEngine) Position() (pos float64) {
	e.do(func(f *audio.Fake) { pos = f.Position() })
	return pos
}

func (e *testEngine) Duration() (dur float64) {
	e.do(func(f *audio.Fake) { dur = f.Duration() })
	return dur
}

// emit reports k about the last load, as the engine would from its own
// goroutine; the daemon posts it to its loop.
func (e *testEngine) emit(k audio.Kind) {
	var loads uint64
	e.do(func(f *audio.Fake) { loads = f.Loads })
	e.events(audio.Event{Kind: k, Load: loads})
}

// loads returns how many streams the engine loaded, 0 before it was made.
func (e *testEngine) loads() (n uint64) {
	e.do(func(f *audio.Fake) { n = f.Loads })
	return n
}

// isPlaying reports whether the engine plays.
func (e *testEngine) isPlaying() (playing bool) {
	e.do(func(f *audio.Fake) { playing = f.Playing })
	return playing
}

// newTester shows a on a Tester that runs the Client's posts before every
// build.
func newTester(a *app) *ui.Tester {
	y := ytmdOf(a)
	tt := ui.NewTester(func(c *ui.Context) {
		y.runClient()
		a.view(c)
	}, 1360, 860)
	tt.SetTitleBar(ui.TitleBar{Height: a.theme.Layout.TitleBar, Left: 78})
	tt.Frame()
	return tt
}

// eventually draws frames and runs posts until cond holds: for what the
// daemon does on its own goroutines, as resolving a stream.
func eventually(t testing.TB, tt *ui.Tester, q posts, what string, cond func() bool) {
	t.Helper()
	timeout := time.After(wait)
	for !cond() {
		select {
		case fn := <-q.app:
			fn()
		case fn := <-q.client:
			fn()
		case <-time.After(time.Millisecond):
		case <-timeout:
			t.Fatalf("never %s, texts %q", what, tt.Texts())
		}
		tt.Frame()
	}
}

// peer connects another front-end to q's ytmd, as `ytm` would: it greets
// without subscribing, and calls what it likes.
func peer(tb testing.TB, q posts) *remote.Client {
	tb.Helper()
	c, _, err := remote.Connect(tb.Context(), pipeDialer{q.ytmd.svc}, false, api.RoleCLI, version.Dev)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(c.Close)
	return c
}

// call has c's daemon answer cl, failing tb otherwise.
func call(tb testing.TB, c *remote.Client, cl api.Call) {
	tb.Helper()
	if _, err := c.Call(context.Background(), cl); err != nil {
		tb.Fatalf("%s: %v", cl.Method, err)
	}
}
