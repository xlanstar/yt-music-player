// Package loop runs funcs one at a time on one goroutine, in the order they
// were posted: the daemon's thread, which owns the player, the playback,
// the account and the engine as the window's main thread owns them in
// Desktop. Post is its post and its playback's Wake.
//
// Post never waits on the loop: it appends to a queue under a mutex held
// for the append alone. The loop sleeps on a channel while the queue is
// empty, so an idle loop wakes nothing.
package loop

import "sync"

// initialCap holds the funcs of a usual batch: a request and the events
// it causes.
const initialCap = 16

// Loop is a run queue; New makes one, Run runs it.
type Loop struct {
	after func()

	mu      sync.Mutex
	queue   []func() // FIFO; Run swaps it with its emptied batch
	stopped bool

	wake chan struct{} // the queue has funcs or Stop was called; buffered 1
}

// New returns a Loop. after, if not nil, runs on the loop after each batch
// of funcs, those posted while the previous batch ran: the daemon there
// sends what they changed, once per batch rather than per func.
func New(after func()) *Loop {
	return &Loop{
		after: after,
		queue: make([]func(), 0, initialCap),
		wake:  make(chan struct{}, 1),
	}
}

// Post queues fn to run on the loop, from any goroutine. After Stop it
// drops fn: nothing runs any more.
func (l *Loop) Post(fn func()) {
	l.mu.Lock()
	if l.stopped {
		l.mu.Unlock()
		return
	}
	l.queue = append(l.queue, fn)
	l.mu.Unlock()
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

// Run runs the funcs posted, on the calling goroutine, until Stop; it
// returns once the func running when Stop is called returns, dropping the
// funcs still queued. Call it once.
func (l *Loop) Run() {
	batch := make([]func(), 0, initialCap)
	for {
		l.mu.Lock()
		batch, l.queue = l.queue, batch
		stopped := l.stopped
		l.mu.Unlock()
		if stopped {
			return
		}
		if len(batch) == 0 {
			<-l.wake
			continue
		}
		for i, fn := range batch {
			if l.isStopped() {
				return
			}
			fn()
			// Drops what fn captured as soon as it ran.
			batch[i] = nil
		}
		batch = batch[:0]
		if l.after != nil && !l.isStopped() {
			l.after()
		}
	}
}

func (l *Loop) isStopped() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stopped
}

// Stop has Run return and Post drop its funcs from now on. It returns at
// once, so the loop may call it too.
func (l *Loop) Stop() {
	l.mu.Lock()
	l.stopped = true
	clear(l.queue)
	l.queue = l.queue[:0]
	l.mu.Unlock()
	select {
	case l.wake <- struct{}{}:
	default:
	}
}
