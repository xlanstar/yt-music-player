package tui

import (
	"testing"
	"yt-music-player/internal/api"
)

// viewCases are screens as Update renders them: a long queue playing
// (which draws only the rows that show). allocs is each one's budget (TestBudgetView).
var viewCases = []struct {
	name    string
	prepare func(h *harness)
	allocs  float64
}{
	// 95 allocs today.
	{"queue-5000", func(h *harness) {
		songs := longQueue(5000)
		h.mirror.setQueue(songs)
		h.mirror.state.Playing, h.mirror.state.Index, h.mirror.state.Current = true, 2500, songs[2500].ID
		h.mirror.state.Shuffled, h.mirror.state.Repeat = true, api.RepeatAll
	}, 110},
}

// viewing is a screen of tc, rendered once.
func viewing(tb testing.TB, prepare func(h *harness)) *harness {
	h := placeholderHarness(tb)
	prepare(h)
	h.send(postedMsg{})
	return h
}

var sinkContent string

// BenchmarkView renders a screen, as Update does after a message.
func BenchmarkView(b *testing.B) {
	for _, tc := range viewCases {
		b.Run(tc.name, func(b *testing.B) {
			h := viewing(b, tc.prepare)
			b.ReportAllocs()
			for b.Loop() {
				h.m.render()
			}
			sinkContent = h.m.content
		})
	}
}

// BenchmarkUpdate is the progress's tick a second while playing: an
// update that renders the screen and schedules the next tick.
func BenchmarkUpdate(b *testing.B) {
	h := viewing(b, func(h *harness) { h.mirror.state.Playing = true })
	b.Run("tick", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			h.send(tickMsg{})
		}
		sinkContent = h.m.content
	})
}
