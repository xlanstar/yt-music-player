package loop

import "testing"

// sinkInt keeps the compiler from optimizing measured calls away.
var sinkInt int

// postWait posts fn and waits until it ran: a request's way through the
// daemon's loop.
func postWait(l *Loop, ran chan struct{}, fn func()) {
	l.Post(fn)
	<-ran
}

func BenchmarkPost(b *testing.B) {
	b.Run("queued", func(b *testing.B) {
		// Not running: Post alone, as the queue grows and is dropped.
		l := New(nil)
		fn := func() { sinkInt++ }
		b.ReportAllocs()
		for b.Loop() {
			l.Post(fn)
			if len(l.queue) == 1024 {
				l.queue = l.queue[:0]
			}
		}
	})
	b.Run("round-trip", func(b *testing.B) {
		l := New(nil)
		go l.Run()
		defer l.Stop()
		ran := make(chan struct{})
		fn := func() { ran <- struct{}{} }
		b.ReportAllocs()
		for b.Loop() {
			postWait(l, ran, fn)
		}
	})
}
