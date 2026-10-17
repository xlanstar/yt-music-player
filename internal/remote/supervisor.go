package remote

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/ipc"

	"github.com/rs/zerolog/log"
)

// The Supervisor's times (docs/daemon.md, Health).
const (
	// pingTimeout unanswered makes the daemon Unresponsive.
	pingTimeout = 2 * time.Second
	// HandoffWait is how long a hand-off gives the new daemon to come.
	HandoffWait = 5 * time.Second
	// RestartGrace is how long Restart lets the daemon stop before it
	// kills it.
	RestartGrace = 3 * time.Second
	minBackoff   = 250 * time.Millisecond
	maxBackoff   = 30 * time.Second
	// unavailableAfter failures in a row make the daemon Unavailable.
	unavailableAfter = 3
	exitPoll         = 50 * time.Millisecond
)

// Options configure a Supervisor.
type Options struct {
	Role    api.Role // api.RoleDesktop or api.RoleTUI
	Version string   // this front-end's
	Dialer  Dialer
	// Spawn starts this front-end's own ytmd after a hand-off; nil is
	// ipc.SpawnDaemon.
	Spawn func() error

	// isAlive and kill are the OS's unless a test sets them.
	isAlive func(pid int) bool
	kill    func(pid int) error
}

// Supervisor keeps a Client connected, as docs/daemon.md (Health;
// Versions and hand-off) describes. Nothing runs on a timer while
// connected: idle costs no wake-ups. Its methods are safe for concurrent
// use.
type Supervisor struct {
	c    *Client
	opts Options

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{} // closed when run returns

	// Signals to run, buffered 1.
	retry, foreground, ping, restart chan struct{}

	// isUnresponsive is read on every frame received, so atomic.
	isUnresponsive atomic.Bool

	mu sync.Mutex
	// base is the health under isUnresponsive.
	base Health
	peer Peer
	// connection counts the links opened for calls.
	connection uint64
	// handoffUntil ends another front-end's hand-off: until then,
	// connecting never starts ytmd.
	handoffUntil time.Time

	// run's own.
	handedOff map[string]bool // daemon versions handed off
	backoff   time.Duration

	// shown is the Link last posted; the front-end's thread owns it.
	shown     Link
	publishFn func()
}

// Supervise starts connecting c, and keeps it connected until Close.
func Supervise(c *Client, opts Options) *Supervisor {
	if opts.Spawn == nil {
		opts.Spawn = ipc.SpawnDaemon
	}
	if opts.isAlive == nil {
		opts.isAlive = processAlive
	}
	if opts.kill == nil {
		opts.kill = killProcess
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Supervisor{
		c:          c,
		opts:       opts,
		ctx:        ctx,
		cancel:     cancel,
		done:       make(chan struct{}),
		retry:      make(chan struct{}, 1),
		foreground: make(chan struct{}, 1),
		ping:       make(chan struct{}, 1),
		restart:    make(chan struct{}, 1),
		handedOff:  map[string]bool{},
	}
	s.publishFn = s.publish
	c.sup = s
	go s.run()
	return s
}

// Close stops connecting and closes the connection; the daemon plays on.
func (s *Supervisor) Close() {
	s.cancel()
	<-s.done
}

// Retry connects now, waiting again from the shortest backoff.
func (s *Supervisor) Retry() { signal(s.retry) }

// Foreground tells that the front-end came to the foreground: it pings
// the daemon if connected, else connects now as Retry does.
func (s *Supervisor) Foreground() { signal(s.foreground) }

// Restart stops the daemon (Shutdown, then a kill after RestartGrace) and
// starts another.
func (s *Supervisor) Restart() { signal(s.restart) }

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// heard runs on every frame received: the daemon answers.
func (s *Supervisor) heard() {
	if !s.isUnresponsive.Load() {
		return
	}
	s.mu.Lock()
	changed := s.isUnresponsive.CompareAndSwap(true, false)
	s.mu.Unlock()
	if changed {
		s.c.post(s.publishFn)
	}
}

// handoff runs when another front-end hands off.
func (s *Supervisor) handoff(ev api.HandoffEvent) {
	log.Debug().Int("by", ev.By).Dur("wait", ev.Wait).Msg("remote: another front-end hands off")
	s.mu.Lock()
	s.handoffUntil = time.Now().Add(ev.Wait)
	s.mu.Unlock()
}

// set makes h the health, with the daemon peer, and posts it.
func (s *Supervisor) set(h Health, peer Peer) {
	s.mu.Lock()
	s.base, s.peer = h, peer
	s.isUnresponsive.Store(false)
	s.mu.Unlock()
	s.c.post(s.publishFn)
}

func (s *Supervisor) setUnresponsive() {
	s.mu.Lock()
	s.isUnresponsive.Store(true)
	s.mu.Unlock()
	s.c.post(s.publishFn)
}

// publish hands the latest Link to the front-end, on its thread; posts
// may cross, as each reads the latest.
func (s *Supervisor) publish() {
	s.mu.Lock()
	l := Link{Health: s.base, Daemon: s.peer, Connection: s.connection}
	if s.isUnresponsive.Load() {
		l.Health = Unresponsive
	}
	s.mu.Unlock()
	if l == s.shown {
		return
	}
	s.shown = l
	if s.c.h.Link != nil {
		s.c.h.Link(l)
	}
}

// outcome is how a connection attempt ended.
type outcome uint8

const (
	failed outcome = iota
	connected
	again   // after a hand-off: connect again now
	stopped // the versions forbid working: wait for Retry
)

// run connects and serves connections until Close.
func (s *Supervisor) run() {
	defer close(s.done)
	defer s.c.Close()
	failures := 0
	s.backoff = minBackoff
	for s.ctx.Err() == nil {
		l, out := s.connect()
		switch out {
		case connected:
			failures, s.backoff = 0, minBackoff
			s.serve(l)
			if s.ctx.Err() == nil {
				s.set(Reconnecting, s.peer)
			}
		case again:
		case stopped:
			s.sleep(-1)
		case failed:
			failures++
			if failures >= unavailableAfter {
				s.set(Unavailable, s.peer)
			}
			d := s.backoff
			s.backoff = min(2*d, maxBackoff)
			s.sleep(d)
		}
	}
}

// sleep waits d (forever if negative) or for Retry, Foreground or
// Restart, which also reset the backoff.
func (s *Supervisor) sleep(d time.Duration) {
	var timeout <-chan time.Time
	if d >= 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		timeout = t.C
	}
	select {
	case <-timeout:
		return
	case <-s.ctx.Done():
		return
	case <-s.retry:
	case <-s.foreground:
	case <-s.restart:
	}
	s.backoff = minBackoff
}

// connect dials and greets a daemon, and decides by its versions.
func (s *Supervisor) connect() (*link, outcome) {
	nc, err := s.dial()
	if err != nil {
		log.Debug().Err(err).Msg("remote: could not reach the daemon")
		return nil, failed
	}
	conn := ipc.NewConn(nc)
	h, err := greet(s.ctx, conn, hello(s.opts.Role, s.opts.Version, true))
	if err != nil {
		log.Debug().Err(err).Msg("remote: the daemon did not greet")
		return nil, failed
	}
	peer := compare(h, s.opts.Version)
	switch {
	case peer.IsOlder && !s.handedOff[h.Version]:
		s.handedOff[h.Version] = true
		s.handOff(s.c.open(conn, peer, true))
		return nil, again
	case peer.IsOlder:
		// Still older: the ytmd found is old too.
		log.Debug().Str("daemon", h.Version).Str("ours", s.opts.Version).Msg("remote: the daemon is outdated")
		if !peer.IsCompatible {
			s.set(DaemonOutdated, peer)
			_ = conn.Close()
			return nil, stopped
		}
		return s.use(conn, peer, DaemonOutdated), connected
	case !peer.IsCompatible:
		// A newer daemon, or a development build of another Proto: the
		// one of the lower Proto is outdated.
		health := DaemonOutdated
		if peer.IsNewer || h.Proto > api.Proto {
			health = FrontendOutdated
		}
		log.Debug().Uint16("daemon", h.Proto).Uint16("ours", api.Proto).Msg("remote: another protocol")
		s.set(health, peer)
		_ = conn.Close()
		return nil, stopped
	}
	return s.use(conn, peer, Connected), connected
}

// use has calls go through conn, to the daemon peer, from now on.
func (s *Supervisor) use(conn *ipc.Conn, peer Peer, h Health) *link {
	l := s.c.open(conn, peer, false)
	s.mu.Lock()
	s.connection++
	s.mu.Unlock()
	s.set(h, peer)
	return l
}

// dial connects, starting ytmd if need be; while another front-end hands
// off, it only waits for the daemon that one starts.
func (s *Supervisor) dial() (net.Conn, error) {
	s.mu.Lock()
	wait := time.Until(s.handoffUntil)
	s.mu.Unlock()
	if wait <= 0 {
		return s.opts.Dialer.DialOrSpawn(s.ctx)
	}
	ctx, cancel := context.WithTimeout(s.ctx, wait)
	defer cancel()
	return s.opts.Dialer.Wait(ctx)
}

// handOff has the old daemon of l save and exit, then starts this
// front-end's own ytmd, for which the next dial waits.
func (s *Supervisor) handOff(l *link) {
	log.Debug().Str("daemon", l.peer.Hello.Version).Str("ours", s.opts.Version).Msg("remote: handing off")
	ctx, cancel := context.WithTimeout(s.ctx, HandoffWait)
	err := s.c.untilGone(ctx, l, &api.Call{Method: api.MethodHandoff, Hand: &api.HandoffArgs{Wait: HandoffWait}})
	cancel()
	l.close()
	if err != nil {
		// It may be gone all the same; dialing tells.
		log.Debug().Err(err).Msg("remote: hand-off")
	}
	if s.ctx.Err() != nil {
		return
	}
	if err := s.opts.Spawn(); err != nil {
		log.Debug().Err(err).Msg("remote: could not start ytmd after a hand-off")
	}
	s.mu.Lock()
	s.handoffUntil = time.Now().Add(HandoffWait)
	s.mu.Unlock()
}

// serve waits on l until it ends, pinging the daemon when asked to.
func (s *Supervisor) serve(l *link) {
	for {
		select {
		case <-s.ctx.Done():
			l.close()
			return
		case <-l.closed:
			return
		case <-s.retry:
			// Nothing to retry while connected.
		case <-s.foreground:
			s.pingOn(l)
		case <-s.ping:
			s.pingOn(l)
		case <-s.restart:
			s.restartOn(l)
			return
		}
	}
}

// pingOn pings the daemon of l; a lost connection shows on l.closed.
func (s *Supervisor) pingOn(l *link) {
	ctx, cancel := context.WithTimeout(s.ctx, pingTimeout)
	defer cancel()
	_, err := s.c.callOn(ctx, l, &api.Call{Method: api.MethodPing})
	if errors.Is(err, context.DeadlineExceeded) && s.ctx.Err() == nil {
		log.Debug().Msg("remote: the daemon does not answer")
		s.setUnresponsive()
	}
}

// restartOn stops the daemon of l; the next connection starts another.
func (s *Supervisor) restartOn(l *link) {
	ctx, cancel := context.WithTimeout(s.ctx, RestartGrace)
	defer cancel()
	if err := s.c.untilGone(ctx, l, &api.Call{Method: api.MethodShutdown}); err != nil {
		log.Debug().Err(err).Msg("remote: shutdown")
	}
	l.close()
	pid := l.peer.Hello.PID
	if pid <= 0 {
		return
	}
	// The daemon closes the connection before it exits: wait for that.
	t := time.NewTicker(exitPoll)
	defer t.Stop()
	isAlive := s.opts.isAlive(pid)
	for isAlive && ctx.Err() == nil {
		select {
		case <-t.C:
			isAlive = s.opts.isAlive(pid)
		case <-ctx.Done():
		}
	}
	if !isAlive || s.ctx.Err() != nil {
		return
	}
	if err := s.opts.kill(pid); err != nil {
		log.Debug().Err(err).Int("pid", pid).Msg("remote: could not kill the daemon")
	}
	s.backoff = minBackoff
}
