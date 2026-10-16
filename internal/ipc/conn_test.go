package ipc

import (
	"context"
	"errors"
	"io"
	"net"
	"reflect"
	"testing"
	"testing/synctest"
	"time"
	"yt-music-player/internal/api"
)

// connPair is two Conns over net.Pipe, closed when the test ends.
func connPair(t testing.TB) (a, b *Conn) {
	t.Helper()
	ca, cb := net.Pipe()
	a, b = NewConn(ca), NewConn(cb)
	t.Cleanup(func() {
		_ = a.Close()
		_ = b.Close()
	})
	return a, b
}

func stateFrame(position float64) api.Frame {
	return api.Frame{Event: &api.Event{State: &api.State{
		Playing: true, Position: position, Duration: 200, Volume: 0.5, Repeat: api.RepeatAll, Current: "song", Index: 1,
	}}}
}

func likesFrame(id string) api.Frame {
	return api.Frame{Event: &api.Event{Likes: &api.Likes{Added: []string{id}}}}
}

func receive(t *testing.T, c *Conn) api.Frame {
	t.Helper()
	var f api.Frame
	if err := c.Receive(&f); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	return f
}

func TestConnRoundTrip(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		front, daemon := connPair(t)
		call := api.Frame{ID: 1, Call: &api.Call{Method: api.MethodSearch, Query: &api.QueryArgs{Query: "lofi"}}}
		if err := front.Send(call); err != nil {
			t.Fatal(err)
		}
		if got := receive(t, daemon); !reflect.DeepEqual(got, call) {
			t.Errorf("daemon got %+v, want %+v", got, call)
		}
		reply := api.Frame{ID: 1, Reply: &api.Reply{Err: &api.Error{Code: api.CodeNotFound}}}
		if err := daemon.Send(reply); err != nil {
			t.Fatal(err)
		}
		if got := receive(t, front); !reflect.DeepEqual(got, reply) {
			t.Errorf("front-end got %+v, want %+v", got, reply)
		}
	})
}

// A field of the previous frame never stays.
func TestConnReceiveZeroes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, b := connPair(t)
		hello := api.Frame{Hello: &api.Hello{Proto: api.Proto, Role: api.RoleDaemon}}
		for _, f := range []api.Frame{hello, stateFrame(1)} {
			if err := a.Send(f); err != nil {
				t.Fatal(err)
			}
		}
		var f api.Frame
		if err := b.Receive(&f); err != nil {
			t.Fatal(err)
		}
		kept := f.Hello
		if err := b.Receive(&f); err != nil {
			t.Fatal(err)
		}
		if f.Hello != nil || f.Event == nil {
			t.Errorf("second frame %+v", f)
		}
		if !reflect.DeepEqual(kept, hello.Hello) {
			t.Errorf("the first frame's Hello became %+v", kept)
		}
	})
}

// A peer that never reads never blocks Send: the queue fills, and the
// connection closes.
func TestConnBackpressure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		nc, peer := net.Pipe()
		c := NewConn(nc)
		defer func() {
			_ = c.Close()
			_ = peer.Close()
		}()
		sent := 0
		var err error
		for range 1000 {
			if err = c.Send(likesFrame("x")); err != nil {
				break
			}
			sent++
		}
		if !errors.Is(err, ErrOverflow) {
			t.Fatalf("after %d frames: err %v, want ErrOverflow", sent, err)
		}
		// The writer took one batch before blocking; the queue holds more.
		if sent < QueueLen || sent > 2*QueueLen {
			t.Errorf("overflowed after %d frames", sent)
		}
		if err := c.Send(likesFrame("y")); !errors.Is(err, ErrOverflow) {
			t.Errorf("Send after overflow: %v", err)
		}
		var f api.Frame
		if err := c.Receive(&f); !errors.Is(err, ErrOverflow) {
			t.Errorf("Receive after overflow: %v", err)
		}
		// The peer finds the connection closed.
		if _, err := io.ReadAll(peer); err != nil {
			t.Errorf("peer read: %v", err)
		}
	})
}

// Only the newest State waits; it goes last, so it never overtakes a reply
// queued before it. Other events stay, a State among others too.
func TestConnCoalescesState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, b := connPair(t)
		if err := a.Send(likesFrame("first")); err != nil {
			t.Fatal(err)
		}
		synctest.Wait() // the writer has taken it and waits for b to read

		withQueue := api.Frame{Event: &api.Event{State: stateFrame(1.5).Event.State, Queue: &api.Queue{IDs: []string{"a"}}}}
		reply := api.Frame{ID: 5, Reply: &api.Reply{}}
		for _, f := range []api.Frame{stateFrame(1), withQueue, reply, stateFrame(2), likesFrame("second"), stateFrame(3)} {
			if err := a.Send(f); err != nil {
				t.Fatal(err)
			}
		}
		for i, want := range []api.Frame{likesFrame("first"), withQueue, reply, likesFrame("second"), stateFrame(3)} {
			if got := receive(t, b); !reflect.DeepEqual(got, want) {
				t.Errorf("frame %d: got %+v, want %+v", i, got, want)
			}
		}
		synctest.Wait()
		if err := b.Shutdown(t.Context()); err != nil {
			t.Error(err)
		}
		var f api.Frame
		if err := a.Receive(&f); !errors.Is(err, io.EOF) {
			t.Errorf("after the peer's Shutdown: %+v, %v; want EOF", f, err)
		}
	})
}

// stateOnly sees every other field of Event: one added there must not be
// coalesced away with its State.
func TestStateOnly(t *testing.T) {
	f := stateFrame(1)
	if !stateOnly(&f) {
		t.Fatal("a State event does not coalesce")
	}
	ev := reflect.ValueOf(f.Event).Elem()
	for i := range ev.NumField() {
		name := ev.Type().Field(i).Name
		if name == "State" {
			continue
		}
		e := *f.Event
		field := reflect.ValueOf(&e).Elem().Field(i)
		switch field.Kind() {
		case reflect.Pointer:
			field.Set(reflect.New(field.Type().Elem()))
		case reflect.Slice:
			field.Set(reflect.MakeSlice(field.Type(), 1, 1))
		default:
			t.Fatalf("Event.%s is a %s", name, field.Kind())
		}
		if stateOnly(&api.Frame{Event: &e}) {
			t.Errorf("a State with %s coalesces", name)
		}
	}
	for _, other := range []api.Frame{
		{ID: 1, Event: f.Event},
		{Event: f.Event, Cancel: true},
		{Event: f.Event, Reply: &api.Reply{}},
		{Event: &api.Event{}},
		{},
	} {
		if stateOnly(&other) {
			t.Errorf("%+v coalesces", other)
		}
	}
}

// Shutdown writes what is queued, then closes; Send fails meanwhile.
func TestConnShutdownDrains(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, b := connPair(t)
		for i := range 10 {
			if err := a.Send(likesFrame(string(rune('a' + i)))); err != nil {
				t.Fatal(err)
			}
		}
		done := make(chan error)
		go func() { done <- a.Shutdown(context.Background()) }()
		for i := range 10 {
			if got := receive(t, b); got.Event.Likes.Added[0] != string(rune('a'+i)) {
				t.Errorf("frame %d: %+v", i, got.Event.Likes)
			}
		}
		if err := <-done; err != nil {
			t.Errorf("Shutdown: %v", err)
		}
		var f api.Frame
		if err := b.Receive(&f); !errors.Is(err, io.EOF) {
			t.Errorf("after the frames: %v, want EOF", err)
		}
		if err := a.Send(likesFrame("late")); !errors.Is(err, ErrClosed) {
			t.Errorf("Send after Shutdown: %v", err)
		}
	})
}

// A peer that never reads holds Shutdown only until its ctx ends.
func TestConnShutdownGivesUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		nc, peer := net.Pipe()
		defer func() { _ = peer.Close() }()
		c := NewConn(nc)
		if err := c.Send(likesFrame("x")); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := c.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Shutdown: %v, want DeadlineExceeded", err)
		}
		if err := c.Send(likesFrame("y")); !errors.Is(err, ErrClosed) {
			t.Errorf("Send after: %v", err)
		}
	})
}

// Close stops the writer and a Receive under way; the bubble fails on any
// goroutine left.
func TestConnCloseStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, b := connPair(t)
		received := make(chan error)
		go func() {
			var f api.Frame
			received <- b.Receive(&f)
		}()
		synctest.Wait()
		if err := b.Close(); err != nil {
			t.Fatal(err)
		}
		if err := <-received; !errors.Is(err, ErrClosed) {
			t.Errorf("Receive under way: %v, want ErrClosed", err)
		}
		if err := b.Close(); err != nil {
			t.Errorf("second Close: %v", err)
		}
		if err := b.Send(likesFrame("x")); !errors.Is(err, ErrClosed) {
			t.Errorf("Send after Close: %v", err)
		}
		var f api.Frame
		if err := a.Receive(&f); !errors.Is(err, io.EOF) {
			t.Errorf("peer: %v, want EOF", err)
		}
		if err := a.Send(likesFrame("x")); !errors.Is(err, io.EOF) {
			t.Errorf("peer's Send: %v, want EOF", err)
		}
	})
}

// Over a real socket, as the daemon serves.
func TestConnOverSocket(t *testing.T) {
	l, path := listen(t)
	accepted := make(chan *Conn, 1)
	go func() {
		nc, err := l.Accept()
		if err != nil {
			t.Error(err)
			close(accepted)
			return
		}
		accepted <- NewConn(nc)
	}()
	nc, err := Dial(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	front := NewConn(nc)
	defer func() { _ = front.Close() }()
	daemon := <-accepted
	if daemon == nil {
		t.FailNow()
	}
	defer func() { _ = daemon.Close() }()
	if err := daemon.Send(stateFrame(4)); err != nil {
		t.Fatal(err)
	}
	if got, want := receive(t, front), stateFrame(4); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
