package daemon

import (
	"context"
	"net"
	"strconv"
	"sync"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/ipc"
	"yt-music-player/internal/version"

	"github.com/rs/zerolog/log"
)

// sender is where a connection's frames go: an ipc.Conn, or a sink in
// budgets, which hold no socket.
type sender interface{ Send(api.Frame) error }

// conn is a front-end's connection, as the loop knows it.
type conn struct {
	out   sender
	hello api.Hello
	// isCompatible: the peer speaks this Proto, else it may only Ping,
	// Handoff and Shutdown.
	isCompatible bool
	isSubscribed bool
	// calls cancel its catalog requests and SignIn in flight, by ID, as a
	// Cancel or the connection's end does.
	calls map[uint32]func()
}

// send sends f; false if it failed, as every later send will: the
// connection closed.
func (c *conn) send(f api.Frame) bool {
	if err := c.out.Send(f); err != nil {
		log.Debug().Int("pid", c.hello.PID).Err(err).Msg("send")
		return false
	}
	return true
}

func (c *conn) reply(id uint32, r *api.Reply) { c.send(api.Frame{ID: id, Reply: r}) }

// replyAhead sends r after the parts that go ahead of it, up to the first
// send that fails.
func (c *conn) replyAhead(id uint32, ahead []*api.Ahead, r *api.Reply) {
	for _, a := range ahead {
		if !c.send(api.Frame{ID: id, Reply: &api.Reply{Ahead: a}}) {
			return
		}
	}
	c.reply(id, r)
}

func (c *conn) fail(id uint32, err error) { c.reply(id, &api.Reply{Err: api.Encode(err)}) }

func (c *conn) cancelAll() {
	for id, cancel := range c.calls {
		cancel()
		delete(c.calls, id)
	}
}

// Serve runs a connection until it ends, and closes nc: Open's listener
// serves each, tests one end of a net.Pipe.
func (s *Service) Serve(nc net.Conn) {
	ic := ipc.NewConn(nc)
	defer ic.Close() //nolint:errcheck // always nil
	if !s.live.track(ic) {
		return
	}
	defer s.live.untrack(ic)
	var f api.Frame
	if err := ic.Receive(&f); err != nil {
		log.Debug().Err(err).Msg("hello")
		return
	}
	if f.Hello == nil {
		log.Debug().Msg("first frame is no hello")
		return
	}
	if err := f.Hello.Validate(); err != nil {
		log.Debug().Err(err).Msg("hello")
		return
	}
	c := &conn{
		out:          ic,
		hello:        *f.Hello,
		isCompatible: f.Hello.Proto == api.Proto,
		calls:        map[uint32]func(){},
	}
	s.post(func() { s.register(c) })
	defer s.post(func() { s.unregister(c) })
	for {
		if err := ic.Receive(&f); err != nil {
			return
		}
		id := f.ID
		switch {
		case f.Cancel:
			s.post(func() { s.cancelCall(c, id) })
		case f.Call != nil:
			cl := f.Call
			s.post(func() { s.dispatch(c, id, cl) })
		}
	}
}

// liveConns are the connections Serve runs, which a stopping Service
// closes; safe from any goroutine.
type liveConns struct {
	mu       sync.Mutex
	conns    map[*ipc.Conn]struct{}
	isClosed bool // no more connections
}

// track adds ic; false once drained.
func (l *liveConns) track(ic *ipc.Conn) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.isClosed {
		return false
	}
	if l.conns == nil {
		l.conns = map[*ipc.Conn]struct{}{}
	}
	l.conns[ic] = struct{}{}
	return true
}

func (l *liveConns) untrack(ic *ipc.Conn) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.conns, ic)
}

// drain takes no more connections and shuts down those live, giving them
// up to timeout to flush what was sent.
func (l *liveConns) drain(timeout time.Duration) {
	l.mu.Lock()
	l.isClosed = true
	live := make([]*ipc.Conn, 0, len(l.conns))
	for c := range l.conns {
		live = append(live, c)
	}
	l.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var wg sync.WaitGroup
	for _, c := range live {
		wg.Go(func() { _ = c.Shutdown(ctx) })
	}
	wg.Wait()
}

func (s *Service) register(c *conn) {
	if s.isStopping {
		return
	}
	s.conns[c] = struct{}{}
	c.send(api.Frame{Hello: &api.Hello{
		Proto: api.Proto, Version: version.Version, Role: api.RoleDaemon, PID: s.pid,
	}})
	if c.hello.Subscribe && c.isCompatible {
		c.isSubscribed = true
		for _, f := range s.snapshot() {
			c.send(f)
		}
		// Its snapshot only says the fetch failed (offline at login, say),
		// which nothing else fetches again.
		if s.acct.Failed() != nil && !s.acct.IsFetching() {
			s.acct.Fetch()
		}
	}
}

func (s *Service) unregister(c *conn) {
	c.cancelAll()
	delete(s.conns, c)
}

func (s *Service) cancelCall(c *conn, id uint32) {
	if cancel, ok := c.calls[id]; ok {
		cancel()
		delete(c.calls, id)
	}
}

// broadcast sends ev to every subscriber, which share it, after the songs
// carry moves out of it.
func (s *Service) broadcast(ev *api.Event) {
	ahead := carry(nil, ev)
	for c := range s.conns {
		if !c.isSubscribed {
			continue
		}
		for _, f := range ahead {
			c.send(f)
		}
		c.send(api.Frame{Event: ev})
	}
}

// errProto answers a peer of another Proto asking for what it cannot.
var errProto = &api.Error{Code: api.CodeInternal, Detail: "ytmd speaks protocol " + strconv.Itoa(int(api.Proto))}
