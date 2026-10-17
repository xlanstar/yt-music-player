package ipc

import (
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
