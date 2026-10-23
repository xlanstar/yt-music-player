package ipc

import (
	"net"
	"testing"
	"yt-music-player/internal/api"
	"yt-music-player/internal/budget"
)

// TestBudgetStateFrame holds a State event's way from Send to Receive, its
// types sent before. All of it is gob's Decoder, which reads each message
// into a new buffer and each pointer into new memory: the receiver keeps
// them (a front-end posts them to its UI thread), so no pool can help.
func TestBudgetStateFrame(t *testing.T) {
	daemon, front := connPair(t)
	state := stateFrame(1)
	var f api.Frame
	// 4 allocs today: the message, Event, State, State.Current.
	budget.Allocs(t, 5, 200, func() {
		if err := daemon.Send(state); err != nil {
			t.Fatal(err)
		}
		if err := front.Receive(&f); err != nil {
			t.Fatal(err)
		}
	})
}

// TestBudgetLiveIdle holds the heap a connection keeps both ways, idle once
// each side greeted the other and a State event went: with the two Conns,
// less once they closed.
func TestBudgetLiveIdle(t *testing.T) {
	if budget.Race {
		t.Skip("the race detector allocates on its own")
	}
	ca, cb := net.Pipe()
	daemon, front := NewConn(ca), NewConn(cb)
	hello := api.Frame{Hello: &api.Hello{Proto: api.Proto, Version: "1.2.3", Role: api.RoleDaemon, PID: 1}}
	call := api.Frame{ID: 1, Call: &api.Call{Method: api.MethodPing}}
	var f api.Frame
	for _, step := range []struct {
		from, to *Conn
		f        api.Frame
	}{{front, daemon, hello}, {daemon, front, hello}, {daemon, front, stateFrame(1)}, {front, daemon, call}} {
		if err := step.from.Send(step.f); err != nil {
			t.Fatal(err)
		}
		if err := step.to.Receive(&f); err != nil {
			t.Fatal(err)
		}
	}
	f = api.Frame{}
	with := budget.LiveHeap()
	_ = daemon.Close()
	_ = front.Close()
	live := (with - budget.LiveHeap()) / 1024
	t.Logf("%d KB live", live)
	// 143 to 148 KB today, gob's two decoders most of it, each compiling
	// the whole Frame; 4 KB socket buffers and queues of QueueLen took 185.
	if live > 170 {
		t.Errorf("%d KB live, budget 170", live)
	}
}
