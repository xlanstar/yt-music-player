package remote

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/ipc"
	"yt-music-player/internal/notice"

	"github.com/rs/zerolog/log"
)

// controlTimeout is how long a control waits for its reply before it is
// told as failed and the daemon pinged.
const controlTimeout = 2 * time.Second

var errNoResult = fmt.Errorf("%w: a reply without its result", api.ErrInvalid)

// Handler gets what the daemon reports besides playback, on the
// front-end's thread. A nil func drops its reports.
type Handler struct {
	// Account, Library, Likes and Failed go to the session's Apply methods.
	Account func(api.AccountEvent)
	Library func(catalog.Library)
	Likes   func(api.Likes)
	Failed  func(api.FailedEvent)
	// Notice gets the daemon's notices and notice.ControlFailed.
	Notice func(notice.Notice)
	// Link gets the Supervisor's latest Link when it changed: changes in
	// quick succession may show as one.
	Link func(Link)
}

// Client is a front-end's end of the daemon's socket. Without a
// connection, every call fails with api.ErrDisconnected.
//
// Its methods are safe for concurrent use. Those that take a ctx block
// until the reply, so run them off the UI thread; when ctx ends first
// they return ctx.Err() and tell the daemon to cancel.
type Client struct {
	post func(func())
	h    Handler
	// sup is set before the first connection; nil without a Supervisor.
	sup *Supervisor
	// flushFn is c.flush, made once rather than on every post.
	flushFn func()

	mu    sync.Mutex
	link  *link // calls go through it; nil when there is none
	links uint64

	evMu sync.Mutex
	// queue holds the events and callbacks not yet flushed, in the order
	// their frames came.
	queue    []received
	isPosted bool
	// spare is the batch flushed last, reused for the next one; the
	// front-end's thread owns it.
	spare []received

	mirror Mirror
}

// link is one connection to the daemon.
type link struct {
	n    uint64 // the Client's count of it
	conn *ipc.Conn
	peer Peer
	// isQuiet drops events: the daemon is about to go away.
	isQuiet bool
	// closed is closed once the connection is gone and every call on it
	// failed.
	closed chan struct{}

	// Guarded by Client.mu.
	lastID  uint32
	pending map[uint32]*pending
}

// pending is a call not yet answered: a blocking call's (ch), a Go's
// (done), or else a control's.
type pending struct {
	ch    chan result
	done  func(*api.Reply, error)
	timer *time.Timer // a control's
	// ctx is a Go's; stop stops watching it.
	ctx  context.Context
	stop func() bool
}

type result struct {
	reply *api.Reply
	err   error
}

// received is an event, when and over which connection it came, or a
// callback due.
type received struct {
	ev   *api.Event
	link uint64
	at   time.Time
	fn   func()
}

// NewClient returns a Client without a connection. post runs a func on
// the front-end's thread, in order; it must not block (as
// tea.Program.Send does): the Client's goroutines call it, also while
// that thread waits for them in Close.
func NewClient(post func(func()), h Handler) *Client {
	c := &Client{post: post, h: h}
	c.flushFn = c.flush
	return c
}

// Mirror returns the daemon's playback; read it on the front-end's thread.
func (c *Client) Mirror() *Mirror { return &c.mirror }

// Close closes the connection, failing the calls in flight; under a
// Supervisor, Supervisor.Close does.
func (c *Client) Close() {
	c.mu.Lock()
	l := c.link
	c.mu.Unlock()
	if l != nil {
		l.close()
	}
}

// open makes a link of conn, whose daemon greeted as peer, and starts
// reading it. Unless quiet, calls go through it from now on.
func (c *Client) open(conn *ipc.Conn, peer Peer, isQuiet bool) *link {
	c.mu.Lock()
	c.links++
	l := &link{
		n:       c.links,
		conn:    conn,
		peer:    peer,
		isQuiet: isQuiet,
		closed:  make(chan struct{}),
		pending: map[uint32]*pending{},
	}
	old := c.link
	if !isQuiet {
		c.link = l
	}
	c.mu.Unlock()
	if old != nil && !isQuiet {
		old.close()
	}
	go c.read(l)
	return l
}

// close closes l and waits until every call on it failed.
func (l *link) close() {
	_ = l.conn.Close()
	<-l.closed
}

func (c *Client) current() *link {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.link
}

// read is l's reader goroutine.
func (c *Client) read(l *link) {
	defer close(l.closed)
	// Receive zeroes f and allocates what it points to anew, so pointers
	// taken out of it stay valid.
	var f api.Frame
	for {
		if err := l.conn.Receive(&f); err != nil {
			c.drop(l, err)
			return
		}
		if c.sup != nil {
			c.sup.heard()
		}
		switch {
		case f.Reply != nil:
			if p := c.take(l, f.ID); p != nil {
				c.finish(p, f.Reply, f.Reply.Err.Err())
			}
		case f.Event != nil:
			c.receive(l, f.Event)
		}
	}
}

// drop fails every call on l, which closed for err.
func (c *Client) drop(l *link, err error) {
	c.mu.Lock()
	if c.link == l {
		c.link = nil
	}
	calls := l.pending
	l.pending = nil
	c.mu.Unlock()
	log.Debug().Err(err).Uint64("link", l.n).Int("pending", len(calls)).Msg("remote: disconnected")
	for _, p := range calls {
		c.finish(p, nil, api.ErrDisconnected)
	}
}

// take removes call id of l from those pending; nil if it was not.
func (c *Client) take(l *link, id uint32) *pending {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := l.pending[id]
	if p == nil {
		return nil
	}
	delete(l.pending, id)
	if p.timer != nil {
		p.timer.Stop()
	}
	if p.stop != nil {
		p.stop()
	}
	return p
}

// finish ends a call taken out of those pending.
func (c *Client) finish(p *pending, reply *api.Reply, err error) {
	switch {
	case p.ch != nil:
		p.ch <- result{reply: reply, err: err}
	case p.done != nil:
		c.later(func() { p.done(reply, err) })
	case err != nil:
		c.later(func() { c.tell(controlFailed(err)) })
	}
}

// send registers p as call's and sends it over l, or the current link if
// l is nil; it returns the link and the call's ID.
func (c *Client) send(l *link, call *api.Call, p *pending) (*link, uint32, error) {
	if err := call.Validate(); err != nil {
		return nil, 0, err
	}
	c.mu.Lock()
	if l == nil {
		l = c.link
	}
	if l == nil || l.pending == nil {
		c.mu.Unlock()
		return nil, 0, api.ErrDisconnected
	}
	l.lastID++
	if l.lastID == 0 { // after 4 billion calls
		l.lastID = 1
	}
	id := l.lastID
	l.pending[id] = p
	// Set before the call goes, so before its reply.
	switch {
	case p.ctx != nil:
		p.stop = context.AfterFunc(p.ctx, func() { c.abandon(l, id) })
	case p.ch == nil && p.done == nil:
		p.timer = time.AfterFunc(controlTimeout, func() { c.expire(l, id) })
	}
	c.mu.Unlock()
	if err := l.conn.Send(api.Frame{ID: id, Call: call}); err != nil {
		if c.take(l, id) == nil {
			// The reader failed it already.
			return nil, 0, api.ErrDisconnected
		}
		return nil, 0, fmt.Errorf("%w: %w", api.ErrDisconnected, err)
	}
	return l, id, nil
}

// abandon ends Go's call id of l, whose ctx ended.
func (c *Client) abandon(l *link, id uint32) {
	p := c.take(l, id)
	if p == nil {
		return
	}
	_ = l.conn.Send(api.Frame{ID: id, Cancel: true})
	err := p.ctx.Err()
	c.later(func() { p.done(nil, err) })
}

// expire ends control id of l, unanswered in time.
func (c *Client) expire(l *link, id uint32) {
	if c.take(l, id) == nil {
		return
	}
	_ = l.conn.Send(api.Frame{ID: id, Cancel: true})
	log.Debug().Uint32("id", id).Msg("remote: no reply to a control")
	c.later(func() { c.tell(controlFailed(context.DeadlineExceeded)) })
	if c.sup != nil {
		signal(c.sup.ping)
	}
}

// Call sends call and waits for its reply, for calls without a typed
// method (a command's controls, which must not return before the daemon
// took them).
func (c *Client) Call(ctx context.Context, call api.Call) (*api.Reply, error) {
	return c.callOn(ctx, nil, &call)
}

// callOn is Call over l, the current link if nil.
func (c *Client) callOn(ctx context.Context, l *link, call *api.Call) (*api.Reply, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p := &pending{ch: make(chan result, 1)}
	l, id, err := c.send(l, call, p)
	if err != nil {
		return nil, err
	}
	select {
	case r := <-p.ch:
		if r.err != nil {
			return nil, r.err
		}
		return r.reply, nil
	case <-ctx.Done():
		if c.take(l, id) != nil {
			_ = l.conn.Send(api.Frame{ID: id, Cancel: true})
		}
		return nil, ctx.Err()
	}
}

// Go sends call without waiting. done gets the reply or the error on the
// front-end's thread, before the events the call caused, which a blocking
// call cannot promise. When ctx ends first, the daemon is told to cancel
// and done gets ctx.Err().
func (c *Client) Go(ctx context.Context, call api.Call, done func(*api.Reply, error)) {
	if err := ctx.Err(); err != nil {
		c.later(func() { done(nil, err) })
		return
	}
	if _, _, err := c.send(nil, &call, &pending{done: done, ctx: ctx}); err != nil {
		c.later(func() { done(nil, err) })
	}
}

// control sends call without waiting; a failure is told.
func (c *Client) control(call *api.Call) {
	if _, _, err := c.send(nil, call, &pending{}); err != nil {
		c.later(func() { c.tell(controlFailed(err)) })
	}
}

// controlFailed is the notice of a control that failed for err; only the
// daemon's own errors add a detail, as the Link tells of a lost
// connection.
func controlFailed(err error) notice.Notice {
	n := notice.Notice{Kind: notice.Failure, Title: notice.ControlFailed, Topic: notice.Control}
	if e, ok := errors.AsType[*api.Error](err); ok && e.Detail != "" {
		n.Detail, n.Args = notice.Reason, []string{e.Detail}
	}
	return n
}

func (c *Client) tell(n notice.Notice) {
	if c.h.Notice != nil {
		c.h.Notice(n)
	}
}

// receive queues ev for the front-end's thread; a burst of events costs
// one post.
func (c *Client) receive(l *link, ev *api.Event) {
	if ev.Handoff != nil && c.sup != nil {
		c.sup.handoff(*ev.Handoff)
	}
	if l.isQuiet {
		return
	}
	c.enqueue(received{ev: ev, link: l.n, at: time.Now()})
}

// later runs fn on the front-end's thread, after what came before.
func (c *Client) later(fn func()) { c.enqueue(received{fn: fn}) }

func (c *Client) enqueue(r received) {
	c.evMu.Lock()
	c.queue = append(c.queue, r)
	isPosted := c.isPosted
	c.isPosted = true
	c.evMu.Unlock()
	if !isPosted {
		c.post(c.flushFn)
	}
}

// flush applies what was queued, on the front-end's thread.
func (c *Client) flush() {
	c.evMu.Lock()
	batch := c.queue
	c.queue, c.spare = c.spare[:0], nil
	c.isPosted = false
	c.evMu.Unlock()
	for i := range batch {
		c.apply(&batch[i])
	}
	clear(batch)
	c.spare = batch[:0]
}

// apply keeps r's playback in the Mirror and hands the rest to the
// Handler, the account first: a sign-in drops what came before it.
func (c *Client) apply(r *received) {
	if r.fn != nil {
		r.fn()
		return
	}
	ev := r.ev
	c.mirror.apply(ev, r.link, r.at)
	if ev.Account != nil && c.h.Account != nil {
		c.h.Account(*ev.Account)
	}
	if ev.Library != nil && c.h.Library != nil {
		c.h.Library(*ev.Library)
	}
	if ev.Likes != nil && c.h.Likes != nil {
		c.h.Likes(*ev.Likes)
	}
	if ev.Failed != nil && c.h.Failed != nil {
		c.h.Failed(*ev.Failed)
	}
	if ev.Notice != nil {
		c.tell(*ev.Notice)
	}
}
