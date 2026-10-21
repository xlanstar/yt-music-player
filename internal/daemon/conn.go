package daemon

import (
	"net"
	"strconv"
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
	// compatible: the peer speaks this Proto, else it may only Ping,
	// Handoff and Shutdown.
	compatible bool
	subscribed bool
	// calls cancel its catalog requests and SignIn in flight, by ID, as a
	// Cancel or the connection's end does.
	calls map[uint32]func()
}

func (c *conn) send(f api.Frame) {
	if err := c.out.Send(f); err != nil {
		log.Debug().Int("pid", c.hello.PID).Err(err).Msg("send")
	}
}

func (c *conn) reply(id uint32, r *api.Reply) { c.send(api.Frame{ID: id, Reply: r}) }

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
		calls:      map[uint32]func(){},
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
		Proto: api.Proto, Version: version.Version, Role: api.RoleDaemon, PID: s.pid,
	}})
	if c.hello.Subscribe && c.compatible {
		c.subscribed = true
		for _, f := range s.snapshot() {
			c.send(f)
		}
		s.fetchLibrary()
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
		if !c.subscribed {
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
