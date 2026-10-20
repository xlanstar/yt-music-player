package tui

import (
	"fmt"
	"image"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/remote"
	"yt-music-player/internal/termimage"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// find returns the cell where text first shows on the screen.
func (h *harness) find(text string) (x, y int) {
	h.t.Helper()
	for y, line := range strings.Split(h.screen(), "\n") {
		if before, _, ok := strings.Cut(line, text); ok {
			return h.m.cells.width(before), y
		}
	}
	h.t.Fatalf("the screen lacks %q:\n%s", text, h.screen())
	return 0, 0
}

// clickOn clicks the cell where text shows.
func (h *harness) clickOn(text string) tea.Cmd {
	h.t.Helper()
	x, y := h.find(text)
	return h.click(x, y)
}

func (h *harness) click(x, y int) tea.Cmd {
	h.t.Helper()
	return h.send(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
}

// progressBar is where the screen shows the progress's bar: its line, its
// first cell and its length.
func (h *harness) progressBar() (y, start, n int) {
	h.t.Helper()
	for y, line := range strings.Split(h.screen(), "\n") {
		i := strings.IndexAny(line, "━─")
		if i < 0 || !strings.HasPrefix(line, " ") || strings.Count(line, " ") < 3 {
			continue
		}
		bar := strings.TrimRight(line[i:], " 0123456789:")
		if strings.Trim(bar, "━─") != "" {
			continue
		}
		return y, h.m.cells.width(line[:i]), h.m.cells.width(bar)
	}
	h.t.Fatalf("no progress on the screen:\n%s", h.screen())
	return 0, 0, 0
}

// selectedLine is the screen's line of the selected row.
func (h *harness) selectedLine() string {
	h.t.Helper()
	for line := range strings.Lines(h.screen()) {
		if strings.HasPrefix(line, " ▸ ") {
			return line
		}
	}
	return ""
}

func (h *harness) wantCalls(what string, want ...string) {
	h.t.Helper()
	if got := h.ctl.take(); !reflect.DeepEqual(got, want) {
		h.t.Errorf("%s sent %q, want %q", what, got, want)
	}
}

func TestClickSelects(t *testing.T) {
	h := placeholderHarness(t)
	h.clickOn("Paper Moons")
	if !strings.Contains(h.selectedLine(), "Paper Moons") {
		t.Errorf("the click selected %q", h.selectedLine())
	}
	h.wantCalls("a click on a row")

	// Clicked again, later, the selected row plays.
	h.clock = h.clock.Add(time.Second)
	h.clickOn("Paper Moons")
	h.wantCalls("a click on the selected row", "Play paper-moons 0")
}

func TestDoubleClick(t *testing.T) {
	h := placeholderHarness(t)
	h.clickOn("Static Bloom")
	h.clock = h.clock.Add(200 * time.Millisecond)
	h.clickOn("Static Bloom")
	h.wantCalls("a double click", "Play static-bloom 0")

	// A triple click plays once.
	h.clock = h.clock.Add(200 * time.Millisecond)
	h.clickOn("Static Bloom")
	h.wantCalls("a third click")
}

func TestClickCurrentPlays(t *testing.T) {
	h := placeholderHarness(t)
	// The cursor follows the current song: it is the selected row.
	h.clickOn("Midnight Arcade")
	h.wantCalls("a click on the selected row", "Play midnight-arcade 0")
}

func TestClickLibrary(t *testing.T) {
	h := placeholderHarness(t)
	h.clickOn("Library")
	h.screenHas("Playlists", "Albums")
	// Liked music is selected: a click opens it, and Play all plays.
	h.clickOn("Liked music")
	h.loaded()
	h.clock = h.clock.Add(time.Second)
	h.clickOn("Play all")
	if got := h.ctl.take(); len(got) != 1 || !strings.HasPrefix(got[0], "PlayAll ") {
		t.Errorf("a click on Play all sent %q", got)
	}
}

func TestClickTabs(t *testing.T) {
	h := placeholderHarness(t)
	for _, tc := range []struct {
		label string
		want  tab
	}{{"Search", tabSearch}, {"Library", tabLibrary}, {"Queue", tabQueue}} {
		x, y := h.find(tc.label)
		// The tab's cell: a space either side of its name.
		h.click(x-1, y)
		if h.m.tab != tc.want {
			t.Errorf("a click on %s showed tab %d", tc.label, h.m.tab)
		}
	}
	h.press("?")
	h.clickOn("Library")
	if h.m.tab != tabLibrary || h.m.isHelp {
		t.Errorf("a tab clicked over the help: tab %d, help %t", h.m.tab, h.m.isHelp)
	}
}

func TestClickSearch(t *testing.T) {
	h := placeholderHarness(t)
	h.clickOn("Search")
	h.clickOn("Search songs")
	if !h.m.isTyping {
		t.Fatal("a click on the input did not focus it")
	}
	h.typeText("moon")
	h.loaded()
	// A click on a result leaves the input and only selects it.
	h.clickOn("Slow Burn Summer")
	if h.m.isTyping {
		t.Error("a click on a result left the input focused")
	}
	if !strings.Contains(h.selectedLine(), "Slow Burn Summer") {
		t.Errorf("the click selected %q", h.selectedLine())
	}
	h.wantCalls("a click while typing")
	h.clock = h.clock.Add(time.Second)
	h.clickOn("Slow Burn Summer")
	h.wantCalls("a click on the selected result", "Play slow-burn-summer 0")
}

func TestClickSeeks(t *testing.T) {
	h := placeholderHarness(t)
	y, start, n := h.progressBar()
	dur := h.mirror.state.Duration
	k := n / 4
	h.click(start+k, y)
	h.wantCalls("a press on the progress")
	h.send(tea.MouseReleaseMsg{X: start + k, Y: y, Button: tea.MouseLeft})
	calls := h.ctl.take()
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "Seek ") {
		t.Fatalf("a click on the progress sent %q", calls)
	}
	got, err := strconv.ParseFloat(strings.TrimPrefix(calls[0], "Seek "), 64)
	if err != nil {
		t.Fatal(err)
	}
	// The cell's middle, a cell of the song.
	if want := (float64(k) + 0.5) / float64(n) * dur; got < want-dur/float64(n) || got > want+dur/float64(n) {
		t.Errorf("a click at cell %d of %d sought %g, want about %g", k, n, got, want)
	}

	// Outside the bar, a press does nothing.
	h.click(start-2, y)
	h.send(tea.MouseReleaseMsg{X: start - 2, Y: y, Button: tea.MouseLeft})
	h.wantCalls("a click on the elapsed time")
}

func TestDragSeeks(t *testing.T) {
	h := placeholderHarness(t)
	y, start, n := h.progressBar()
	h.click(start+1, y)
	h.send(tea.MouseMotionMsg{X: start + n/2, Y: y, Button: tea.MouseLeft})
	// A State meanwhile does not pull the dragged position back.
	h.mirror.state.Position, h.mirror.gen = 10, h.mirror.gen+1
	h.send(postedMsg{})
	h.send(tea.MouseMotionMsg{X: start + n + 20, Y: y + 3, Button: tea.MouseLeft})
	h.wantCalls("a drag")
	h.screenHas(" 3:47 ━━━━━━━━━━━")
	h.send(tea.MouseReleaseMsg{X: start + n + 20, Y: y + 3, Button: tea.MouseLeft})
	h.wantCalls("a drag let go past the end", "Seek 227")

	// Until a State answers, the position stays where it was let go.
	h.send(postedMsg{})
	h.screenHas(" 3:47 ")
	h.mirror.state.Position, h.mirror.gen = 226, h.mirror.gen+1
	h.send(postedMsg{})
	h.screenHas(" 3:46 ")
}

// A drag whose release the terminal never told ends at the next click.
func TestDragLostRelease(t *testing.T) {
	h := placeholderHarness(t)
	y, start, _ := h.progressBar()
	h.click(start, y)
	h.clickOn("Paper Moons")
	if got := h.ctl.take(); len(got) != 1 || !strings.HasPrefix(got[0], "Seek ") {
		t.Errorf("the next click sent %q, want the seek", got)
	}
}

func TestWheel(t *testing.T) {
	h := placeholderHarness(t)
	h.send(tea.MouseWheelMsg{X: 10, Y: 5, Button: tea.MouseWheelDown})
	if h.m.queue.cursor != wheelRows {
		t.Errorf("the wheel moved the cursor to %d, want %d", h.m.queue.cursor, wheelRows)
	}
	h.send(tea.MouseWheelMsg{X: 10, Y: 5, Button: tea.MouseWheelUp})
	h.send(tea.MouseWheelMsg{X: 10, Y: 5, Button: tea.MouseWheelUp})
	if h.m.queue.cursor != 0 {
		t.Errorf("the wheel moved the cursor to %d, want 0", h.m.queue.cursor)
	}
	h.press("?")
	if h.send(tea.MouseWheelMsg{X: 10, Y: 5, Button: tea.MouseWheelDown}) != nil {
		t.Error("the wheel did something over the help")
	}
}

func TestClickHints(t *testing.T) {
	h := placeholderHarness(t)
	h.clickOn("? Help")
	if !h.m.isHelp {
		t.Fatal("a click on ? Help did not show the help")
	}
	h.clickOn("Keys")
	h.clickOn("Select")
	if !h.m.isHelp {
		t.Error("a click on the help closed it")
	}
	h.clickOn("? Help")
	if h.m.isHelp {
		t.Error("a click on ? Help did not close the help")
	}

	h.m.session.ApplyAccount(api.AccountEvent{SignedIn: false})
	h.clickOn("Library")
	cmd := h.clickOn("Open the Desktop app")
	if cmd == nil {
		t.Fatal("a click on the hint did nothing")
	}
	h.send(runCmd(cmd))
	if h.opened != 1 {
		t.Errorf("opened the Desktop %d times", h.opened)
	}

	h.m.setLink(remote.Link{Health: remote.Unresponsive, Connection: 1})
	h.send(postedMsg{})
	h.clickOn("isn’t responding")
	if !reflect.DeepEqual(h.sup.calls, []string{"Restart"}) {
		t.Errorf("a click on the banner asked %q", h.sup.calls)
	}
}

// TestHitsDrawn holds every hit to what the screen shows in its cells.
func TestHitsDrawn(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(h *harness)
		want    map[hitKind]int
	}{
		{"queue-5000", viewCase("queue-5000"), map[hitKind]int{hitTab: 3, hitRow: 18, hitProgress: 1, hitHelp: 1}},
		{"signed-out", func(h *harness) {
			h.m.session.ApplyAccount(api.AccountEvent{SignedIn: false})
			h.m.setLink(remote.Link{Health: remote.Unresponsive, Connection: 1})
			h.press("shift+tab")
		}, map[hitKind]int{hitTab: 3, hitHint: 1, hitBanner: 1, hitProgress: 1, hitHelp: 1}},
		{"search", func(h *harness) { h.press("tab") }, map[hitKind]int{hitTab: 3, hitInput: 1, hitProgress: 1, hitHelp: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := viewing(t, tc.prepare)
			lines := strings.Split(h.screen(), "\n")
			got := map[hitKind]int{}
			for _, x := range h.m.scene.hits {
				got[x.kind]++
				span := ansi.Cut(lines[x.shown.Min.Y], x.shown.Min.X, x.shown.Max.X)
				var want string
				switch x.kind {
				case hitTab:
					want = []string{" Queue ", " Search ", " Library "}[x.tab]
					if span != want {
						t.Errorf("tab %d covers %q, want %q", x.tab, span, want)
					}
					continue
				case hitHelp:
					if span != "? Help" {
						t.Errorf("the help's key covers %q", span)
					}
					continue
				case hitProgress:
					if y, start, n := h.progressBar(); x.rect != image.Rect(start, y, start+n, y+1) || x.shown != x.rect {
						t.Errorf("the progress's hit is %+v, its bar %d, %d+%d", x, y, start, n)
					}
					continue
				case hitRow:
					want = "Song " + strconv.Itoa(x.i) + " "
				case hitInput:
					want = "Search songs"
				case hitHint:
					want = "Open the Desktop app"
				case hitBanner:
					want = "isn’t responding"
				}
				if !strings.Contains(span, want) {
					t.Errorf("%+v covers %q, want %q", x, span, want)
				}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("hits %v, want %v", got, tc.want)
			}
		})
	}
}

// Every row of a long queue that shows selects its own song.
func TestClickRows(t *testing.T) {
	h := viewing(t, viewCase("queue-5000"))
	_, top := h.find("Song 2483 ")
	for y := top; y < top+18; y++ {
		h.clock = h.clock.Add(time.Second)
		h.click(10, y)
		want := "Song " + strconv.Itoa(2483+y-top) + " "
		if !strings.Contains(h.selectedLine(), want) {
			t.Errorf("a click on line %d selected %q, want %q", y, h.selectedLine(), want)
		}
	}
	h.wantCalls("clicks on rows")
}

// A short terminal cuts the body's last lines, then the bar's first: their
// hits go with them, and those left move up.
func TestClickShortTerminal(t *testing.T) {
	h := placeholderHarness(t)
	h.m.session.ApplyAccount(api.AccountEvent{SignedIn: false})
	h.clickOn("Library")
	_, y := h.find("Open the Desktop app")
	// The body ends above the hint: the bar's rule shows on its line.
	h.resize(80, y+barRows)
	if cmd := h.click(5, y); cmd != nil || h.opened != 0 {
		t.Error("a click where a cut hint was opened the Desktop")
	}

	h.m.setLink(remote.Link{Health: remote.Unresponsive, Connection: 1})
	h.resize(80, 3)
	h.click(5, 0)
	if len(h.sup.calls) != 0 {
		t.Errorf("a click where a cut banner was asked %q", h.sup.calls)
	}
	py, start, n := h.progressBar()
	if py != 1 {
		t.Fatalf("the progress shows on line %d of 3", py)
	}
	h.click(start+n/2, py)
	h.send(tea.MouseReleaseMsg{X: start + n/2, Y: py, Button: tea.MouseLeft})
	if got := h.ctl.take(); len(got) != 1 || !strings.HasPrefix(got[0], "Seek ") {
		t.Errorf("a click on the moved progress sent %q", got)
	}
	h.clickOn("? Help")
	if !h.m.isHelp {
		t.Error("a click on the moved ? Help did nothing")
	}
}

// tabAt is where the tab named label shows: a space either side.
func tabAt(label string) func(h *harness) (y, x0, x1 int) {
	return func(h *harness) (y, x0, x1 int) {
		cell := " " + label + " "
		x0, y = h.find(cell)
		return y, x0, x0 + ansi.StringWidth(cell)
	}
}

// textAt is where text shows.
func textAt(text string) func(h *harness) (y, x0, x1 int) {
	return func(h *harness) (y, x0, x1 int) {
		x0, y = h.find(text)
		return y, x0, x0 + ansi.StringWidth(text)
	}
}

// lineAt is the whole line where text shows.
func lineAt(text string) func(h *harness) (y, x0, x1 int) {
	return func(h *harness) (y, x0, x1 int) {
		_, y = h.find(text)
		return y, 0, h.m.width
	}
}

// progressAt is where the progress's bar shows.
func progressAt(h *harness) (y, x0, x1 int) {
	y, x0, n := h.progressBar()
	return y, x0, x0 + n
}

// TestClickEdges clicks each target on its first and last cells, which
// act, and on the cells either side of it, which do not.
func TestClickEdges(t *testing.T) {
	onTab := func(want tab) func(*harness, tea.Cmd) bool {
		return func(h *harness, _ tea.Cmd) bool { return h.m.tab == want }
	}
	selects := func(text string) func(*harness, tea.Cmd) bool {
		return func(h *harness, _ tea.Cmd) bool { return strings.Contains(h.selectedLine(), text) }
	}
	seeks := func(h *harness, _ tea.Cmd) bool {
		h.send(tea.MouseReleaseMsg{Button: tea.MouseLeft})
		calls := h.ctl.take()
		return len(calls) == 1 && strings.HasPrefix(calls[0], "Seek ")
	}
	showsHelp := func(h *harness, _ tea.Cmd) bool { return h.m.isHelp }
	unresponsive := func(h *harness) { h.m.setLink(remote.Link{Health: remote.Unresponsive, Connection: 1}) }
	cases := []struct {
		name    string
		prepare func(h *harness)
		// at is the target's cells [x0, x1) on line y, as the screen shows it.
		at func(h *harness) (y, x0, x1 int)
		// acted reports that the click, which returned cmd, acted.
		acted func(h *harness, cmd tea.Cmd) bool
	}{
		{"tab-queue", func(h *harness) { h.press("shift+tab") }, tabAt("Queue"), onTab(tabQueue)},
		{"tab-search", nil, tabAt("Search"), onTab(tabSearch)},
		{"tab-library", nil, tabAt("Library"), onTab(tabLibrary)},
		{"tab-queue-narrow", func(h *harness) {
			h.resize(44, 14)
			h.press("shift+tab")
		}, tabAt("Queue"), onTab(tabQueue)},
		{"row", nil, lineAt("Paper Moons"), selects("Paper Moons")},
		{"row-scrolled", func(h *harness) {
			h.mirror.setQueue(longQueue(300))
			h.send(postedMsg{})
			h.press("end")
		}, lineAt("Song 282 "), selects("Song 282 ")},
		{"progress", nil, progressAt, seeks},
		{"progress-cover", func(h *harness) {
			h.withCovers(termimage.Blocks, newCoverHost(h.t))
			h.covered()
		}, progressAt, seeks},
		{"help", nil, textAt("? Help"), showsHelp},
		{"banner", unresponsive, lineAt("isn’t responding"), func(h *harness, _ tea.Cmd) bool {
			return reflect.DeepEqual(h.sup.calls, []string{"Restart"})
		}},
		{"hint", func(h *harness) {
			h.m.session.ApplyAccount(api.AccountEvent{SignedIn: false})
			h.press("shift+tab")
		}, lineAt("Open the Desktop app"), func(_ *harness, cmd tea.Cmd) bool { return cmd != nil }},
		{"input", func(h *harness) { h.press("tab") }, lineAt("Search songs"), func(h *harness, _ tea.Cmd) bool {
			return h.m.isTyping
		}},
		// The bar's top cut: what is left moved up.
		{"short-progress", func(h *harness) { h.resize(80, 2) }, progressAt, seeks},
		{"short-help", func(h *harness) { h.resize(80, 2) }, textAt("? Help"), showsHelp},
		{"short-banner-progress", func(h *harness) {
			unresponsive(h)
			h.resize(80, 3)
		}, progressAt, seeks},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setup := func() *harness {
				h := placeholderHarness(t)
				if tc.prepare != nil {
					tc.prepare(h)
				}
				h.send(postedMsg{})
				return h
			}
			h := setup()
			y, x0, x1 := tc.at(h)
			type probe struct {
				x    int
				want bool
			}
			probes := []probe{{x0, true}, {x1 - 1, true}}
			if x0 > 0 {
				probes = append(probes, probe{x0 - 1, false})
			}
			if x1 < h.m.width {
				probes = append(probes, probe{x1, false})
			}
			for _, p := range probes {
				h := setup()
				if got := tc.acted(h, h.click(p.x, y)); got != p.want {
					t.Errorf("a click on (%d, %d) of [%d, %d) acted %t, want %t:\n%s", p.x, y, x0, x1, got, p.want, h.screen())
				}
			}
		})
	}
}

// TestSeekCells seeks to the middle of the cell clicked, a share of the
// whole bar however much of the bar shows, and a drag past either end to
// that end.
func TestSeekCells(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(h *harness)
	}{
		{"wide", nil},
		{"narrow", func(h *harness) { h.resize(44, 14) }},
		{"short", func(h *harness) { h.resize(80, 2) }},
		{"cover", func(h *harness) {
			h.withCovers(termimage.Blocks, newCoverHost(h.t))
			h.covered()
		}},
		{"emoji", func(h *harness) { h.mirror.setQueue(emojiSongs) }},
		{"emoji by graphemes", func(h *harness) {
			h.send(unicodeCore)
			h.mirror.setQueue(emojiSongs)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := placeholderHarness(t)
			if tc.prepare != nil {
				tc.prepare(h)
			}
			h.send(postedMsg{})
			y, start, n := h.progressBar()
			dur := h.mirror.state.Duration
			seek := func(cells float64) string { return fmt.Sprintf("Seek %g", cells/float64(n)*dur) }
			release := func(x, y int) { h.send(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft}) }
			for _, p := range []struct {
				x    int
				want string
			}{{start, seek(0.5)}, {start + n/2, seek(float64(n/2) + 0.5)}, {start + n - 1, seek(float64(n) - 0.5)}} {
				h.click(p.x, y)
				release(p.x, y)
				h.wantCalls(fmt.Sprintf("a click on cell %d of %d", p.x-start, n), p.want)
			}
			h.click(start+1, y)
			h.send(tea.MouseMotionMsg{X: start - 5, Y: y + 1, Button: tea.MouseLeft})
			release(start-5, y+1)
			h.wantCalls("a drag past the start", "Seek 0")
			h.click(start+1, y)
			h.send(tea.MouseMotionMsg{X: start + n + 5, Y: max(y-1, 0), Button: tea.MouseLeft})
			release(start+n+5, max(y-1, 0))
			h.wantCalls("a drag past the end", fmt.Sprintf("Seek %g", dur))
		})
	}
}

// A selected row clicked again, drawn before its list changed, plays
// nothing once gone: past the queue's end, or with another page shown.
func TestClickStaleRow(t *testing.T) {
	h := viewing(t, viewCase("queue-5000"))
	x, y := h.find("Song 2490 ")
	h.click(x, y)
	h.mirror.setQueue(longQueue(10))
	h.clock = h.clock.Add(time.Second)
	h.click(x, y)
	h.wantCalls("a click on a selected row gone")
	if !strings.Contains(h.selectedLine(), "Song 9 ") {
		t.Errorf("selected %q, want the queue's last song", h.selectedLine())
	}

	h = viewing(t, viewCase("queue-5000"))
	x, y = h.find("Song 2490 ")
	h.click(x, y)
	h.m.tab = tabLibrary
	h.clock = h.clock.Add(time.Second)
	h.click(x, y)
	h.wantCalls("a click on the queue's selected row under the library")
	if h.m.opened[tabLibrary].kind != 0 {
		t.Errorf("opened %+v from a row of the queue", h.m.opened[tabLibrary])
	}
}

// A drag's ratio reads the progress's whole bar, as laid out, though a
// part of it is cut, which no click hits.
func TestSeekWholeBar(t *testing.T) {
	h := placeholderHarness(t)
	bar := hit{rect: image.Rect(10, 5, 30, 6), shown: image.Rect(10, 5, 20, 6), kind: hitProgress}
	h.m.scene.hits = []hit{bar}
	if want := 4.5 / 20 * h.mirror.state.Duration; !h.m.seekAt(14, bar) || h.m.seek.to != want {
		t.Errorf("cell 4 of 20 seeks to %g, want %g", h.m.seek.to, want)
	}
	if _, ok := h.m.hitAt(25, 5); ok {
		t.Error("a click on the bar's cut part hit it")
	}
}
