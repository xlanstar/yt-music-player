package tui

import (
	"testing"
	"yt-music-player/internal/budget"
)

// TestBudgetView holds a screen's render, the work of every message that
// changes it. A list draws only the rows that show: the 5000 songs queued
// cost what 18 do.
func TestBudgetView(t *testing.T) {
	for _, tc := range viewCases {
		t.Run(tc.name, func(t *testing.T) {
			h := viewing(t, tc.prepare)
			budget.Allocs(t, tc.allocs, 100, h.m.render)
		})
	}
}

// TestBudgetUpdate holds the tick a second while playing, and a click
// that selects a row.
func TestBudgetUpdate(t *testing.T) {
	t.Run("tick", func(t *testing.T) {
		h := viewing(t, func(h *harness) { h.mirror.state.Playing = true })
		// 80 allocs today: the render, and the tick's timer.
		budget.Allocs(t, 92, 100, func() { h.send(tickMsg{}) })
	})
	t.Run("click", func(t *testing.T) {
		h := viewing(t, func(*harness) {})
		// 75 allocs today: the render.
		budget.Allocs(t, 86, 100, clicking(h))
	})
}

// TestBudgetClick holds a click's hit test and what it does.
func TestBudgetClick(t *testing.T) {
	for _, tc := range clickCases {
		t.Run(tc.name, func(t *testing.T) {
			h := viewing(t, tc.prepare)
			budget.Allocs(t, tc.allocs, 100, clickingOn(h, tc.targets(h)))
		})
	}
}

// TestBudgetPost holds a post and its run, as each of remote's events
// does.
func TestBudgetPost(t *testing.T) {
	p := newPoster()
	fn := func() {}
	posting := func() {
		p.post(fn)
		<-p.wake
		p.run()
	}
	// The queue and the batch each grow once.
	posting()
	posting()
	// 0 allocs today: the queue takes the last batch's array.
	budget.Allocs(t, 0, 100, posting)
}
