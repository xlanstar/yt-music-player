package ipc

import (
	"net"
	"testing"
	"yt-music-player/internal/api"
)

// serve answers every call on c with an empty reply until c closes, as a
// daemon would.
func serve(c *Conn) {
	var f api.Frame
	for c.Receive(&f) == nil {
		if c.Send(api.Frame{ID: f.ID, Reply: &api.Reply{}}) != nil {
			return
		}
	}
}

// pingRoundTrip sends a Ping and reads its reply.
func pingRoundTrip(tb testing.TB, front *Conn, f *api.Frame, ping api.Frame) {
	if err := front.Send(ping); err != nil {
		tb.Fatal(err)
	}
	if err := front.Receive(f); err != nil {
		tb.Fatal(err)
	}
}

// socketPair is two Conns over a Unix socket.
func socketPair(tb testing.TB) (front, daemon *Conn) {
	tb.Helper()
	l, path := listen(tb)
	accepted := make(chan net.Conn, 1)
	go func() {
		nc, err := l.Accept()
		if err != nil {
			close(accepted)
			return
		}
		accepted <- nc
	}()
	nc, err := Dial(tb.Context(), path)
	if err != nil {
		tb.Fatal(err)
	}
	dc := <-accepted
	if dc == nil {
		tb.Fatal("accept failed")
	}
	front, daemon = NewConn(nc), NewConn(dc)
	tb.Cleanup(func() {
		_ = front.Close()
		_ = daemon.Close()
	})
	return front, daemon
}

func BenchmarkRoundTrip(b *testing.B) {
	for _, tc := range []struct {
		name string
		pair func(testing.TB) (*Conn, *Conn)
	}{
		{"pipe", connPair},
		{"unix", socketPair},
	} {
		b.Run(tc.name, func(b *testing.B) {
			front, daemon := tc.pair(b)
			go serve(daemon)
			ping := api.Frame{ID: 1, Call: &api.Call{Method: api.MethodPing}}
			var f api.Frame
			b.ReportAllocs()
			for b.Loop() {
				pingRoundTrip(b, front, &f, ping)
			}
		})
	}
}

// BenchmarkFanout is a State event sent to each subscriber, until each has
// read it.
func BenchmarkFanout(b *testing.B) {
	b.Run("2", func(b *testing.B) {
		var subscribers []*Conn
		got := make(chan struct{})
		for range 2 {
			daemon, front := connPair(b)
			subscribers = append(subscribers, daemon)
			go func() {
				var f api.Frame
				for front.Receive(&f) == nil {
					got <- struct{}{}
				}
			}()
		}
		state := stateFrame(1)
		b.ReportAllocs()
		for b.Loop() {
			for _, c := range subscribers {
				if err := c.Send(state); err != nil {
					b.Fatal(err)
				}
			}
			for range subscribers {
				<-got
			}
		}
	})
}
