package daemon

import (
	"context"
	"net"
	"strconv"
	"yt-music-player/internal/api"
	"yt-music-player/internal/ipc"

	"github.com/rs/zerolog/log"
)

// sender is where a connection's frames go: an ipc.Conn, or a sink in
// budgets, which hold no socket.
type sender interface {
	Send(api.Frame) error
	Shutdown(ctx context.Context) error
}

// conn is a front-end's connection, as the loop knows it.
type conn struct {
	out   sender
	hello api.Hello
	// compatible: the peer speaks this Proto. Others may only Ping, Handoff
	// and Shutdown.
	compatible bool
	// subscribed: it asked for events and got its snapshot.
	subscribed bool
	// calls are its catalog requests and SignIn in flight, by ID, which a
	// Cancel or the connection's end cancels.
	calls map[uint32]call
}

// call is a request in flight: a catalog request, which ctx tells from a
// later one of the same ID, or a SignIn, without ctx.
type call struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func (c *conn) send(f api.Frame) {
	if err := c.out.Send(f); err != nil {
		log.Debug().Int("pid", c.hello.PID).Err(err).Msg("send")
	}
}

func (c *conn) reply(id uint32, r *api.Reply) { c.send(api.Frame{ID: id, Reply: r}) }

func (c *conn) fail(id uint32, err error) { c.reply(id, &api.Reply{Err: api.Encode(err)}) }

func (c *conn) cancelAll() {
	for id, call := range c.calls {
		call.cancel()
		delete(c.calls, id)
	}
}

// Serve runs a connection until it ends, as Open's listener does for each:
// it reads the peer's Hello, answers with the daemon's (and a snapshot,
// for a subscriber), then hands each request to the loop. In-process
// front-ends (tests, Desktop's in P4) serve one end of a net.Pipe. It
// takes nc, which it closes.
func (s *Service) Serve(nc net.Conn) {
	ic := ipc.NewConn(nc)
	defer ic.Close() //nolint:errcheck // always nil
	if !s.track(ic) {
		return
	}
	defer s.untrack(ic)
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
		out:        ic,
		hello:      *f.Hello,
		compatible: f.Hello.Proto == api.Proto,
		calls:      map[uint32]call{},
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

// track adds ic to the connections a stopping Service closes; false once
// it stopped.
func (s *Service) track(ic *ipc.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.live[ic] = struct{}{}
	return true
}

func (s *Service) untrack(ic *ipc.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.live, ic)
}

func (s *Service) register(c *conn) {
	if s.stopping {
		return
	}
	s.conns[c] = struct{}{}
	c.send(api.Frame{Hello: &api.Hello{
		Proto: api.Proto, Version: s.version, Role: api.RoleDaemon, PID: s.pid,
	}})
	if c.hello.Subscribe && c.compatible {
		c.subscribed = true
		for _, f := range s.snapshot() {
			c.send(f)
		}
	}
}

func (s *Service) unregister(c *conn) {
	c.cancelAll()
	delete(s.conns, c)
}

func (s *Service) cancelCall(c *conn, id uint32) {
	if call, ok := c.calls[id]; ok {
		call.cancel()
		delete(c.calls, id)
	}
}

// broadcast sends ev to every subscriber. They share it: no one may
// modify it once sent.
func (s *Service) broadcast(ev *api.Event) {
	for c := range s.conns {
		if c.subscribed {
			c.send(api.Frame{Event: ev})
		}
	}
}

// errProto answers a peer of another Proto asking for what it cannot.
var errProto = &api.Error{Code: api.CodeInternal, Detail: "ytmd speaks protocol " + strconv.Itoa(int(api.Proto))}
