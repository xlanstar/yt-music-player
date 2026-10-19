package tui

import (
	"context"
	"sync"

	tea "charm.land/bubbletea/v2"
)

// postedMsg tells Update to run the funcs posted since the last one.
type postedMsg struct{}

// poster is the TUI's post: remote's goroutines, the loader's and the
// session's jobs queue funcs for the UI thread, Update. tea.Program.Send
// blocks until Update takes the message, and post must not, so funcs
// queue here and one message, sent by pump, runs all those queued.
type poster struct {
	mu    sync.Mutex
	queue []func()
	// spare is the last batch run, emptied: the next queue, so steady posts
	// allocate nothing. Only run touches it.
	spare []func()
	// wake holds one signal: a post while one waits adds nothing.
	wake chan struct{}
}

func newPoster() *poster { return &poster{wake: make(chan struct{}, 1)} }

// post queues fn for the UI thread; it never blocks.
func (p *poster) post(fn func()) {
	p.mu.Lock()
	p.queue = append(p.queue, fn)
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// run runs the funcs queued, in order, on the UI thread; those they post
// wait for the next postedMsg.
func (p *poster) run() {
	p.mu.Lock()
	batch := p.queue
	p.queue, p.spare = p.spare, nil
	p.mu.Unlock()
	for i, fn := range batch {
		fn()
		// Drops what fn captured as soon as it ran.
		batch[i] = nil
	}
	p.spare = batch[:0]
}

// pump sends a postedMsg through send whenever funcs were posted, until
// ctx ends.
func (p *poster) pump(ctx context.Context, send func(tea.Msg)) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.wake:
			send(postedMsg{})
		}
	}
}
