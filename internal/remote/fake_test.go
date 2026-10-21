package remote

import (
	"context"
	"net"
	"slices"
	"sync"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/ipc"
	"yt-music-player/internal/notice"
)

// daemon is a fake ytmd: it greets with hello and answers calls through
// answer, over connections the dialer makes.
type daemon struct {
	hello api.Hello
	// answer returns the reply to a call, nil for none. Unset, it replies
	// with no result.
	answer func(*api.Call) *api.Reply
	// ahead returns the parts of a call's reply, sent before it.
	ahead func(*api.Call) []*api.Ahead
	// greeted runs once it greeted a connection; it may send events.
	greeted func(*ipc.Conn)
	// then runs after a call's reply went; it may send events.
	then func(*ipc.Conn, *api.Call)

	mu     sync.Mutex
	hellos []api.Hello // the front-ends'
	frames []api.Frame // received after the hellos
	conns  []*ipc.Conn
	// isGone reports a hand-off or shutdown: it takes no connection.
	isGone bool
}

const daemonPID = 4242

func newDaemon(version string) *daemon {
	return &daemon{hello: api.Hello{Proto: api.Proto, Version: version, Role: api.RoleDaemon, PID: daemonPID}}
}

// connect returns the front-end's end of a new connection.
func (d *daemon) connect(ctx context.Context) net.Conn {
	front, back := net.Pipe()
	conn := ipc.NewConn(back)
	d.mu.Lock()
	d.conns = append(d.conns, conn)
	d.mu.Unlock()
	// It outlives the dial.
	go d.serve(context.WithoutCancel(ctx), conn)
	return front
}

func (d *daemon) serve(ctx context.Context, conn *ipc.Conn) {
	var f api.Frame
	if conn.Receive(&f) != nil || f.Hello == nil {
		_ = conn.Close()
		return
	}
	d.mu.Lock()
	d.hellos = append(d.hellos, *f.Hello)
	d.mu.Unlock()
	if conn.Send(api.Frame{Hello: &d.hello}) != nil {
		return
	}
	if d.greeted != nil {
		d.greeted(conn)
	}
	for conn.Receive(&f) == nil {
		d.mu.Lock()
		d.frames = append(d.frames, f)
		d.mu.Unlock()
		if f.Call == nil {
			continue
		}
		reply := &api.Reply{}
		if d.answer != nil {
			reply = d.answer(f.Call)
		}
		if reply != nil && d.ahead != nil {
			for _, a := range d.ahead(f.Call) {
				_ = conn.Send(api.Frame{ID: f.ID, Reply: &api.Reply{Ahead: a}})
			}
		}
		if reply != nil {
			_ = conn.Send(api.Frame{ID: f.ID, Reply: reply})
		}
		if d.then != nil {
			d.then(conn, f.Call)
		}
		if m := f.Call.Method; m == api.MethodHandoff || m == api.MethodShutdown {
			d.mu.Lock()
			d.isGone = true
			d.mu.Unlock()
			_ = conn.Shutdown(ctx)
			return
		}
	}
}

func (d *daemon) gone() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.isGone
}

// send sends f to every connection.
func (d *daemon) send(f api.Frame) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.conns {
		_ = c.Send(f)
	}
}

// exit closes every connection.
func (d *daemon) exit() {
	d.mu.Lock()
	conns := d.conns
	d.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

// calls returns the methods called, in order.
func (d *daemon) calls() []api.Method {
	d.mu.Lock()
	defer d.mu.Unlock()
	var methods []api.Method
	for _, f := range d.frames {
		if f.Call != nil {
			methods = append(methods, f.Call.Method)
		}
	}
	return methods
}

// received returns the frames received after the hellos.
func (d *daemon) received() []api.Frame {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.frames)
}

func (d *daemon) greetings() []api.Hello {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.hellos)
}

// dialer connects to the daemon running, if any; starting one runs the
// next of those queued.
type dialer struct {
	mu      sync.Mutex
	running *daemon
	next    []*daemon
	// log records each dial ("dial", "wait", "spawn") and its time.
	log []dialed
}

type dialed struct {
	what string
	at   time.Time
}

func (d *dialer) note(what string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.log = append(d.log, dialed{what, time.Now()})
}

func (d *dialer) attempt(ctx context.Context) (net.Conn, error) {
	d.mu.Lock()
	running := d.running
	d.mu.Unlock()
	if running == nil || running.gone() {
		return nil, api.ErrDaemonUnavailable
	}
	return running.connect(ctx), nil
}

func (d *dialer) Dial(ctx context.Context) (net.Conn, error) {
	d.note("dial")
	return d.attempt(ctx)
}

// Wait polls as ipc's does, until ctx ends.
func (d *dialer) Wait(ctx context.Context) (net.Conn, error) {
	d.note("wait")
	for {
		if c, err := d.attempt(ctx); err == nil {
			return c, nil
		}
		select {
		case <-time.After(50 * time.Millisecond):
		case <-ctx.Done():
			return nil, api.ErrDaemonUnavailable
		}
	}
}

func (d *dialer) DialOrSpawn(ctx context.Context) (net.Conn, error) {
	d.note("dial")
	if c, err := d.attempt(ctx); err == nil {
		return c, nil
	}
	if err := d.spawn(); err != nil {
		return nil, err
	}
	return d.attempt(ctx)
}

// spawn runs the next daemon queued.
func (d *dialer) spawn() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.log = append(d.log, dialed{"spawn", time.Now()})
	if len(d.next) == 0 {
		return api.ErrDaemonNotFound
	}
	d.running, d.next = d.next[0], d.next[1:]
	return nil
}

func (d *dialer) setRunning(daemon *daemon) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.running = daemon
}

// stop makes the daemon running exit.
func (d *dialer) stop() {
	d.mu.Lock()
	running := d.running
	d.running = nil
	d.mu.Unlock()
	if running != nil {
		running.exit()
	}
}

func (d *dialer) count(what string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, l := range d.log {
		if l.what == what {
			n++
		}
	}
	return n
}

// ui is a front-end's thread: it runs what is posted when the test says.
type ui struct {
	mu     sync.Mutex
	queued []func()
	posts  int
}

func (u *ui) post(fn func()) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.queued = append(u.queued, fn)
	u.posts++
}

// run runs what was posted, and what that posts.
func (u *ui) run() {
	for {
		u.mu.Lock()
		queued := u.queued
		u.queued = nil
		u.mu.Unlock()
		if len(queued) == 0 {
			return
		}
		for _, fn := range queued {
			fn()
		}
	}
}

// front is a front-end: a Client on a ui, and what its Handler got.
type front struct {
	ui      *ui
	client  *Client
	links   []Link
	notices []notice.Notice
	events  []string
}

func newFront() *front {
	f := &front{ui: &ui{}}
	f.client = NewClient(f.ui.post, Handler{
		Account: f,
		Notice:  func(n notice.Notice) { f.notices = append(f.notices, n) },
		Link:    func(l Link) { f.links = append(f.links, l) },
	})
	return f
}

func (f *front) ApplyAccount(api.AccountEvent) { f.events = append(f.events, "account") }
func (f *front) ApplyLikes(api.Likes)          { f.events = append(f.events, "likes") }
func (f *front) ApplyFailed(api.FailedEvent)   { f.events = append(f.events, "failed") }

// healths returns the healths the front-end was told, in order.
func (f *front) healths() []Health {
	hs := make([]Health, len(f.links))
	for i, l := range f.links {
		hs[i] = l.Health
	}
	return hs
}
