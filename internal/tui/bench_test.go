package tui

import (
	"image"
	"testing"
	"yt-music-player/internal/api"
	"yt-music-player/internal/termimage"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// viewCases are screens as Update renders them: a long queue playing
// (which draws only the rows that show), the queue measured by grapheme,
// the library, search results, the help and the queue with covers.
// allocs is each one's budget (TestBudgetView).
var viewCases = []struct {
	name    string
	prepare func(h *harness)
	allocs  float64
}{
	// 34 allocs today.
	{"queue-5000", func(h *harness) {
		songs := longQueue(5000)
		h.mirror.setQueue(songs)
		h.mirror.state.Playing, h.mirror.state.Index, h.mirror.state.Current = true, 2500, songs[2500].ID
		h.mirror.state.Shuffled, h.mirror.state.Repeat = true, api.RepeatAll
	}, 39},
	// 33 allocs today, as by runes: the queue in a terminal that knows
	// mode 2027.
	{"queue-graphemes", func(h *harness) { h.send(unicodeCore) }, 38},
	// 31 allocs today.
	{"library", func(h *harness) { h.press("shift+tab") }, 36},
	// 30 allocs today.
	{"search", func(h *harness) {
		h.press("/")
		h.typeText("o")
		h.loaded()
		h.press("esc")
	}, 35},
	// 13 allocs today.
	{"help", func(h *harness) { h.press("?") }, 15},
	// 33 allocs today, as without covers: one, encoded once, costs a
	// render nothing.
	{"queue-cover-kitty", func(h *harness) {
		h.withCovers(termimage.Kitty, newCoverHost(h.t))
		h.covered()
	}, 38},
	// 33 allocs today.
	{"queue-cover-sixel", func(h *harness) {
		h.withCovers(termimage.Sixel, newCoverHost(h.t))
		h.covered()
	}, 38},
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

// sceneCases are screens of two columns drawn into a scene, a cover at
// the top left and a hit a row: left to right, right first (spans sorted
// as they come), and a large terminal's. drawAllocs and composeAllocs are
// each one's budgets (TestBudgetScene).
var sceneCases = []struct {
	name                      string
	w, h                      int
	isRightFirst              bool
	drawAllocs, composeAllocs float64
}{
	// 0 and 1 allocs today: the rows keep their spans, compose makes the
	// screen's string.
	{"80x24", 80, 24, false, 0, 1},
	{"80x24-right-first", 80, 24, true, 0, 1},
	{"240x70", 240, 70, false, 0, 1},
}

// drawColumns draws a title and an artist a row, in two columns, the
// right first if isRightFirst.
func drawColumns(screen region, isRightFirst bool) {
	w, h := screen.size()
	left, right := screen.cutLeft(w / 2)
	right = right.inset(1, 0, 1, 0)
	cover, left := left.inset(1, 0, 1, 0).cutLeft(5)
	cover.cover(image.Rect(0, 0, 4, 2), slotHeader, coverURL)
	for y := range h {
		if isRightFirst {
			right.text(0, y, "野見祐二 · Lumen Drive", styleMuted)
		}
		left.text(0, y, "猫の恩返し · Midnight", styleBold)
		if !isRightFirst {
			right.text(0, y, "野見祐二 · Lumen Drive", styleMuted)
		}
		screen.hit(image.Rect(0, y, w, y+1), hit{kind: hitRow, i: y})
	}
}

// sceneOf is tc's scene, drawn once, so that its storage has grown.
func sceneOf(w, h int, isRightFirst bool) *scene {
	s := &scene{}
	drawColumns(s.reset(w, h, newCells(ansi.WcWidth)), isRightFirst)
	s.compose(&coverLines)
	return s
}

// BenchmarkSceneDraw draws a screen into a scene: spans measured, cut
// and kept in order, hits and covers kept.
func BenchmarkSceneDraw(b *testing.B) {
	for _, tc := range sceneCases {
		b.Run(tc.name, func(b *testing.B) {
			s := sceneOf(tc.w, tc.h, tc.isRightFirst)
			b.ReportAllocs()
			for b.Loop() {
				drawColumns(s.reset(tc.w, tc.h, s.cells), tc.isRightFirst)
			}
		})
	}
}

// BenchmarkSceneCompose composes a drawn scene into the screen.
func BenchmarkSceneCompose(b *testing.B) {
	for _, tc := range sceneCases {
		b.Run(tc.name, func(b *testing.B) {
			s := sceneOf(tc.w, tc.h, tc.isRightFirst)
			b.ReportAllocs()
			for b.Loop() {
				sinkContent = s.compose(&coverLines)
			}
		})
	}
}
