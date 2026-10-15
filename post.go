package main

import "sync"

// poster runs funcs on the UI thread through the window's Update. The
// first build may run before NewWindow returns, so posts made before
// attach are queued.
type poster struct {
	mu      sync.Mutex
	forward func(fn func())
	pending []func()
}

func (p *poster) post(fn func()) {
	p.mu.Lock()
	forward := p.forward
	if forward == nil {
		p.pending = append(p.pending, fn)
	}
	p.mu.Unlock()
	if forward != nil {
		forward(fn)
	}
}

// attach holds the lock while flushing so concurrent posts keep their
// order.
func (p *poster) attach(forward func(fn func())) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, fn := range p.pending {
		forward(fn)
	}
	p.pending, p.forward = nil, forward
}
