package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/ipc"

	"github.com/rs/zerolog"
)

// logSink holds what every test logged; a test that reads it turns the
// level up (logAtDebug).
var logSink syncBuffer

func TestMain(m *testing.M) {
	// Only main sets the level: tests would otherwise log every failure
	// they provoke.
	zerolog.SetGlobalLevel(zerolog.Disabled)
	logTo(&logSink)
	os.Exit(m.Run())
}

// logAtDebug logs at debug level until the test ends, and returns what
// is logged from now on.
func logAtDebug(t *testing.T) func() string {
	t.Helper()
	start := logSink.Len()
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	t.Cleanup(func() { zerolog.SetGlobalLevel(zerolog.Disabled) })
	return func() string { return logSink.String()[start:] }
}

// syncBuffer is a bytes.Buffer safe for concurrent use.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

// daemon is a fake ytmd: it greets with hello and answers calls, over
// connections its dialer makes.
type daemon struct {
	hello  api.Hello
	status api.Status
	// answer replies to a call, else nil for the usual reply.
	answer func(*api.Call) *api.Reply

	mu    sync.Mutex
	calls []api.Call
	// isGone reports a hand-off or shutdown: it takes no connection.
	isGone bool
}

func newDaemon(version string, pid int) *daemon {
	return &daemon{hello: api.Hello{Proto: api.Proto, Version: version, Role: api.RoleDaemon, PID: pid}}
}

// connect returns ytm's end of a new connection.
func (d *daemon) connect(ctx context.Context) net.Conn {
	front, back := net.Pipe()
	// It outlives the dial.
	go d.serve(context.WithoutCancel(ctx), ipc.NewConn(back))
	return front
}

func (d *daemon) serve(ctx context.Context, conn *ipc.Conn) {
	defer func() { _ = conn.Close() }()
	var f api.Frame
	if conn.Receive(&f) != nil || f.Hello == nil {
		return
	}
	if conn.Send(api.Frame{Hello: &d.hello}) != nil {
		return
	}
	for conn.Receive(&f) == nil {
		if f.Call == nil {
			continue
		}
		d.mu.Lock()
		d.calls = append(d.calls, *f.Call)
		d.mu.Unlock()
		_ = conn.Send(api.Frame{ID: f.ID, Reply: d.reply(f.Call)})
		if m := f.Call.Method; m == api.MethodHandoff || m == api.MethodShutdown {
			d.mu.Lock()
			d.isGone = true
			d.mu.Unlock()
			ctx, cancel := context.WithTimeout(ctx, time.Second)
			_ = conn.Shutdown(ctx)
			cancel()
			return
		}
	}
}

func (d *daemon) reply(call *api.Call) *api.Reply {
	if d.answer != nil {
		if r := d.answer(call); r != nil {
			return r
		}
	}
	switch call.Method {
	case api.MethodStatus:
		s := d.status
		return &api.Reply{Status: &s}
	case api.MethodSignIn:
		return &api.Reply{Account: &catalog.Account{Name: "Lan"}}
	}
	return &api.Reply{}
}

func (d *daemon) gone() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.isGone
}

// methods returns the methods called, in order.
func (d *daemon) methods() []api.Method {
	d.mu.Lock()
	defer d.mu.Unlock()
	var ms []api.Method
	for _, c := range d.calls {
		ms = append(ms, c.Method)
	}
	return ms
}

// last returns the last call.
func (d *daemon) last(t *testing.T) api.Call {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.calls) == 0 {
		t.Fatal("no call")
	}
	return d.calls[len(d.calls)-1]
}

// errNoSocket is a dial without a daemon, as ipc.Dial's ENOENT.
var errNoSocket = errors.New("dial unix ytmd.sock: no such file or directory")

// dialer reaches the daemon running, if any; a spawn runs the next one
// queued, if any.
type dialer struct {
	mu      sync.Mutex
	running *daemon
	next    []*daemon
	spawns  int
}

func (d *dialer) attempt(ctx context.Context) (net.Conn, error) {
	d.mu.Lock()
	running := d.running
	d.mu.Unlock()
	if running == nil || running.gone() {
		return nil, errNoSocket
	}
	return running.connect(ctx), nil
}

func (d *dialer) Dial(ctx context.Context) (net.Conn, error) { return d.attempt(ctx) }

// Wait polls, as ipc's does, for a second at most.
func (d *dialer) Wait(ctx context.Context) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	for {
		if c, err := d.attempt(ctx); err == nil {
			return c, nil
		}
		select {
		case <-time.After(5 * time.Millisecond):
		case <-ctx.Done():
			return nil, api.ErrDaemonUnavailable
		}
	}
}

func (d *dialer) DialOrSpawn(ctx context.Context) (net.Conn, error) {
	if c, err := d.attempt(ctx); err == nil {
		return c, nil
	}
	if err := d.spawn(); err != nil {
		return nil, err
	}
	return d.Wait(ctx)
}

// spawn runs the next daemon queued; without one, ytmd is not found.
func (d *dialer) spawn() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.spawns++
	if len(d.next) == 0 {
		return api.ErrDaemonNotFound
	}
	d.running, d.next = d.next[0], d.next[1:]
	return nil
}

func (d *dialer) spawned() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.spawns
}

// world is ytm's surroundings in a test: a dialer, the terminal.
type world struct {
	dialer *dialer
	env    *env
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

// ytmVersion is the ytm under test's version.
const ytmVersion = "1.0.0"

// newWorld returns a world where running runs, nil for none, and spawns
// start next in turn.
func newWorld(running *daemon, next ...*daemon) *world {
	w := &world{
		dialer: &dialer{running: running, next: next},
		stdout: &bytes.Buffer{},
		stderr: &bytes.Buffer{},
	}
	w.env = &env{
		stdin:      strings.NewReader(""),
		stdout:     w.stdout,
		stderr:     w.stderr,
		isTerminal: func() bool { return false },
		rawInput:   func() (func(), error) { return nil, errors.New("not a terminal") },
		paths:      ipc.Paths{Socket: "ytmd.sock", Logs: filepath.Join("cache", "logs")},
		dialer:     w.dialer,
		version:    ytmVersion,
	}
	w.env.spawn = w.dialer.spawn
	return w
}

// run runs ytm with args and returns its exit code.
func (w *world) run(t *testing.T, args ...string) int {
	t.Helper()
	return run(t.Context(), args, w.env)
}
