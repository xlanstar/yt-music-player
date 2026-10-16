package ipc

import (
	"testing"
	"yt-music-player/internal/api"
	"yt-music-player/internal/budget"
)

// TestBudgetStateFrame holds a State event's way from Send to Receive, on
// connections that sent one before (gob's types are sent once).
//
// Sending allocates nothing (gob's Encoder reuses its buffers). Receiving
// cannot do with less than 4, all gob's: its Decoder reads each message
// into a new buffer, and decodes a pointer into new memory, so with the
// Frame zeroed first each frame brings a new Event, a new State and the
// string of State.Current. Those are the receiver's to keep (a front-end
// posts them to its UI thread), so they cannot come from a pool.
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
