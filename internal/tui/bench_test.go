package tui

import (
	"testing"
	"yt-music-player/internal/api"

	tea "charm.land/bubbletea/v2"
)

// viewCases are screens as Update renders them: a long queue playing
// (which draws only the rows that show), the library, search results and
// the help. allocs is each one's budget (TestBudgetView).
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
	// 74 allocs today.
	{"library", func(h *harness) { h.press("shift+tab") }, 86},
	// 88 allocs today.
	{"search", func(h *harness) {
		h.press("/")
		h.typeText("o")
		h.loaded()
		h.press("esc")
	}, 102},
	// 100 allocs today.
	{"help", func(h *harness) { h.press("?") }, 113},
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

// BenchmarkUpdate is the progress's tick a second while playing (an
// update that renders the screen and schedules the next tick), and a
// click that selects a row of the queue.
func BenchmarkUpdate(b *testing.B) {
	h := viewing(b, func(h *harness) { h.mirror.state.Playing = true })
	b.Run("tick", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			h.send(tickMsg{})
		}
		sinkContent = h.m.content
	})
	b.Run("click", func(b *testing.B) {
		h := viewing(b, func(*harness) {})
		click := clicking(h)
		b.ReportAllocs()
		for b.Loop() {
			click()
		}
		sinkContent = h.m.content
	})
}

// clicking returns a click on the queue's second or third row, in turn:
// each selects, none plays.
func clicking(h *harness) func() {
	_, y := h.find("Satellite Hearts")
	i := 0
	return func() {
		i++
		h.send(tea.MouseClickMsg{X: 10, Y: y + i%2, Button: tea.MouseLeft})
	}
}

// clickCases are clicks without the render after them, on two targets in
// turn: rows of a long queue, the tabs, the progress. allocs is each
// one's budget (TestBudgetClick).
var clickCases = []struct {
	name    string
	prepare func(h *harness)
	targets func(h *harness) [2]tea.Mouse
	allocs  float64
}{
	// 0 allocs today.
	{"row-queue-5000", viewCases[0].prepare, func(h *harness) [2]tea.Mouse {
		_, y := h.find("Song 2490 ")
		return [2]tea.Mouse{leftAt(10, y), leftAt(10, y+1)}
	}, 0},
	// 0 allocs today.
	{"tab", func(*harness) {}, func(h *harness) [2]tea.Mouse {
		x, y := h.find("Search")
		xq, _ := h.find("Queue")
		return [2]tea.Mouse{leftAt(x, y), leftAt(xq, y)}
	}, 0},
	// 0 allocs today.
	{"progress", func(*harness) {}, func(h *harness) [2]tea.Mouse {
		y, start, n := h.progressBar()
		return [2]tea.Mouse{leftAt(start+n/3, y), leftAt(start+2*n/3, y)}
	}, 0},
}

func leftAt(x, y int) tea.Mouse { return tea.Mouse{X: x, Y: y, Button: tea.MouseLeft} }

// clickingOn returns a click on targets in turn, each a first click,
// none dragging.
func clickingOn(h *harness, targets [2]tea.Mouse) func() {
	i := 0
	return func() {
		i++
		h.m.lastClick, h.m.seek = click{}, seeking{}
		if ok, _ := h.m.click(targets[i%2]); !ok {
			h.t.Fatal("the click did nothing")
		}
	}
}

// BenchmarkClick is a click's hit test and what it does.
func BenchmarkClick(b *testing.B) {
	for _, tc := range clickCases {
		b.Run(tc.name, func(b *testing.B) {
			h := viewing(b, tc.prepare)
			click := clickingOn(h, tc.targets(h))
			b.ReportAllocs()
			for b.Loop() {
				click()
			}
		})
	}
}
