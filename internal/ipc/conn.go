package ipc

import (
	"bufio"
	"context"
	"encoding/gob"
	"errors"
	"net"
	"slices"
	"sync"
	"yt-music-player/internal/api"
)

// QueueLen is how many frames a Conn holds for a peer that reads slower
// than they come; one more closes the connection, and the peer, connecting
// again, gets a full snapshot.
const QueueLen = 64

var (
	// ErrClosed is returned by a Conn closed by Close or Shutdown.
	ErrClosed = errors.New("ipc: connection closed")
	// ErrOverflow is why a Conn closed when its peer read too slowly.
	ErrOverflow = errors.New("ipc: peer reads too slowly")
)

// Conn carries frames over a connection, one gob stream each way. Send never
// blocks, so the daemon's loop never waits on a socket; a sent frame, and what
// it points to, must not change. Frames go out in order, but a newer State
// event replaces one still queued. Receive reads on the caller's goroutine.
type Conn struct {
	nc  net.Conn
	dec *gob.Decoder

	mu    sync.Mutex
	queue []api.Frame // FIFO; the writer swaps it with its emptied batch
	// state is the index in queue of the State event not yet taken by
	// the writer, -1 if none.
	state int
	// draining is set by Shutdown: the writer closes once queue is empty.
	draining bool
	err      error // why the Conn closed; nil while open

	wake    chan struct{} // the writer has frames to take; buffered 1
	quit    chan struct{} // closed with the Conn
	written chan struct{} // closed when the writer returns
}

// NewConn returns a Conn over nc, which it owns from now on, and starts its
// writer.
func NewConn(nc net.Conn) *Conn {
	c := &Conn{
		nc:      nc,
		dec:     gob.NewDecoder(nc),
		queue:   make([]api.Frame, 0, QueueLen),
		state:   -1,
		wake:    make(chan struct{}, 1),
		quit:    make(chan struct{}),
		written: make(chan struct{}),
	}
	go c.write()
	return c
}

// Send queues f to be written, never waiting. It fails once the Conn is
// closed or shutting down; a full queue closes it with ErrOverflow.
func (c *Conn) Send(f api.Frame) error {
	isState := stateOnly(&f)
	c.mu.Lock()
	switch {
	case c.err != nil:
		err := c.err
		c.mu.Unlock()
		return err
	case c.draining:
		c.mu.Unlock()
		return ErrClosed
	}
	if isState && c.state >= 0 {
		// The newer goes last, after whatever was queued before it, so it
		// never overtakes a reply it follows.
		c.queue = slices.Delete(c.queue, c.state, c.state+1)
		c.state = -1
	}
	if len(c.queue) == QueueLen {
		c.mu.Unlock()
		c.close(ErrOverflow)
		return ErrOverflow
	}
	if isState {
		c.state = len(c.queue)
	}
	c.queue = append(c.queue, f)
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
	return nil
}

// stateOnly reports whether f is an event of the State alone, which a newer
// one makes useless.
func stateOnly(f *api.Frame) bool {
	e := f.Event
	return e != nil && e.State != nil && e.Queue == nil && e.Songs == nil && e.Likes == nil &&
		e.Account == nil && e.Library == nil && e.Failed == nil && e.Notice == nil && e.Handoff == nil
}

// write is the writer goroutine: it takes the queued frames, encodes them
// and flushes them, until the Conn closes.
func (c *Conn) write() {
	defer close(c.written)
	bw := bufio.NewWriter(c.nc)
	enc := gob.NewEncoder(bw)
	batch := make([]api.Frame, 0, QueueLen)
	for {
		c.mu.Lock()
		batch, c.queue = c.queue, batch
		c.state = -1
		draining := c.draining
		c.mu.Unlock()

		if len(batch) == 0 {
			if draining {
				c.close(ErrClosed)
				return
			}
			select {
			case <-c.wake:
				continue
			case <-c.quit:
				return
			}
		}
		for i := range batch {
			if err := enc.Encode(&batch[i]); err != nil {
				c.close(err)
				return
			}
		}
		// Drops the frames' data, which the queue would otherwise keep.
		clear(batch)
		batch = batch[:0]
		if err := bw.Flush(); err != nil {
			c.close(err)
			return
		}
	}
}

// Receive reads the next frame into f, zeroed first, as gob sends no zero
// values. What f points to is new memory, which the caller may hand to
// another goroutine. Call it from one goroutine at a time. An error closes
// the Conn; it is why the Conn closed (io.EOF when the peer did).
func (c *Conn) Receive(f *api.Frame) error {
	*f = api.Frame{}
	if err := c.dec.Decode(f); err != nil {
		c.close(err)
		return c.cause()
	}
	return nil
}

// cause returns why the Conn closed, nil while it is open.
func (c *Conn) cause() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close closes the Conn, dropping the frames not yet written, and waits
// for its writer to return. It is safe to call more than once.
func (c *Conn) Close() error {
	c.close(ErrClosed)
	<-c.written
	return nil
}

// Shutdown writes the frames queued, then closes the Conn, so that an
// exiting daemon's last words arrive; Send fails meanwhile. When ctx ends
// first, it closes the Conn at once and returns ctx's error; otherwise
// what closed the Conn before it drained, nil for nothing.
func (c *Conn) Shutdown(ctx context.Context) error {
	c.mu.Lock()
	c.draining = true
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
	select {
	case <-c.written:
	case <-ctx.Done():
		_ = c.Close()
		return ctx.Err()
	}
	if err := c.cause(); !errors.Is(err, ErrClosed) {
		return err
	}
	return nil
}

// close closes the Conn for err, unless it is closed already, unblocking
// the writer and Receive.
func (c *Conn) close(err error) {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return
	}
	c.err = err
	clear(c.queue)
	c.queue = c.queue[:0]
	c.mu.Unlock()
	close(c.quit)
	_ = c.nc.Close()
}
