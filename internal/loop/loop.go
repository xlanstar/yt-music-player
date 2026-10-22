// Package loop runs funcs one at a time on one goroutine, in the order
// they were posted: ytmd's thread, which owns the player, playback, account
// and engine. Post never waits on the loop, and an idle loop wakes nothing.
package loop

import "sync"

// initialCap holds the funcs of a usual batch: a request and the events
// it causes.
const initialCap = 16

// Loop is a run queue; New makes one, Run runs it.
type Loop struct {
	after func()

	mu         sync.Mutex
	queue      []func() // FIFO; Run swaps it with its emptied batch
	hasStopped bool

	wake chan struct{} // the queue has funcs or Stop was called; buffered 1
}

// New returns a Loop. after, if not nil, runs after each batch of funcs
// (those posted while the previous batch ran), so the daemon sends what
// they changed once per batch.
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
	if l.hasStopped {
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
		stopped := l.hasStopped
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
	return l.hasStopped
}

// Stop has Run return and Post drop its funcs from now on. It returns at
// once, so the loop may call it too.
func (l *Loop) Stop() {
	l.mu.Lock()
	l.hasStopped = true
	clear(l.queue)
	l.queue = l.queue[:0]
	l.mu.Unlock()
	select {
	case l.wake <- struct{}{}:
	default:
	}
}
