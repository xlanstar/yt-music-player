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

// TestBudgetUpdate holds the tick a second while playing.
func TestBudgetUpdate(t *testing.T) {
	h := viewing(t, func(h *harness) { h.mirror.state.Playing = true })
	// 100 allocs today: the render, and the tick's timer.
	budget.Allocs(t, 115, 100, func() { h.send(tickMsg{}) })
}
