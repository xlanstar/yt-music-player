package tui

import (
	"context"
	"image"
	"image/color"
	"io"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/termimage"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// program is a harness's model run by a tea.Program as Run runs it, its
// terminal a pipe that answers synchronized output (mode 2026) as kitty
// does; what the Program wrote collects in got.
type program struct {
	t   *testing.T
	h   *harness
	p   *tea.Program
	out *termOut
	// ran closes once the Program returned, read once all it wrote was.
	ran, read chan struct{}
	quitOnce  sync.Once

	mu  sync.Mutex
	got []byte
	// grew signals got grew.
	grew chan struct{}
}

// runTerm runs h's model on an 80×24 terminal, until the test ends.
func runTerm(t *testing.T, h *harness) *program {
	t.Helper()
	return runTermAnswering(t, h, "")
}

// runTermAnswering is runTerm on a terminal that sends answers, such as
// a mode's report, before it reports mode 2026.
func runTermAnswering(t *testing.T, h *harness, answers string) *program {
	t.Helper()
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	tm := &program{t: t, h: h, out: newTermOut(outW), ran: make(chan struct{}), read: make(chan struct{}), grew: make(chan struct{}, 1)}
	h.m.covers.out = tm.out
	if g := &h.m.covers; g.table != nil {
		// Published before there was a terminal.
		tm.out.publish(g.table)
	}
	ctx, cancel := context.WithCancel(context.Background())
	synced := make(chan struct{})
	opts := append(programOptions(tm.out),
		tea.WithContext(ctx),
		tea.WithInput(inR),
		tea.WithWindowSize(80, 24),
		// No TERM_PROGRAM nor SSH_TTY: Bubble Tea asks for mode 2026.
		tea.WithEnvironment([]string{"TERM=xterm-256color"}),
		tea.WithoutSignalHandler(),
		tea.WithFilter(func(m tea.Model, msg tea.Msg) tea.Msg {
			if r, ok := msg.(tea.ModeReportMsg); ok && r.Mode == ansi.ModeSynchronizedOutput {
				close(synced)
			}
			return tm.out.filter(m, msg)
		}),
	)
	tm.p = tea.NewProgram(h.m, opts...)
	go func() {
		defer close(tm.read)
		buf := make([]byte, 64<<10)
		for {
			n, err := outR.Read(buf)
			tm.mu.Lock()
			tm.got = append(tm.got, buf[:n]...)
			tm.mu.Unlock()
			select {
			case tm.grew <- struct{}{}:
			default:
			}
			if err != nil {
				return
			}
		}
	}()
	go h.m.posts.pump(ctx, tm.p.Send)
	go func() {
		defer close(tm.ran)
		if _, err := tm.p.Run(); err != nil && ctx.Err() == nil {
			t.Errorf("the Program failed: %v", err)
		}
	}()
	t.Cleanup(func() {
		tm.quit()
		cancel()
		for _, f := range []*os.File{inR, inW, outR} {
			_ = f.Close()
		}
		if n := tm.out.missing.Load(); n != 0 {
			t.Errorf("%d frames' tables were gone", n)
		}
	})
	if _, err := io.WriteString(inW, answers+"\x1b[?2026;2$y"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-synced:
	case <-time.After(5 * time.Second):
		t.Fatal("Bubble Tea never read the answer")
	}
	// The renderer takes the answer before the next message, which repaints
	// the screen whole: the frames from it on are synchronized.
	tm.p.Send(tea.ClearScreen())
	tm.waitFor("synchronized output", 0, func(s string) bool { return strings.Contains(s, "\x1b[?2026l") })
	return tm
}

// quit quits the Program and returns all it wrote.
func (tm *program) quit() string {
	tm.quitOnce.Do(func() {
		tm.p.Quit()
		select {
		case <-tm.ran:
		case <-time.After(5 * time.Second):
			tm.t.Fatal("the Program never quit")
		}
		_ = tm.out.Close()
		<-tm.read
	})
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return string(tm.got)
}

// written returns what the Program wrote from byte from on.
func (tm *program) written(from int) string {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return string(tm.got[from:])
}

// waitFor waits until what the Program wrote from byte from on is ok, and
// returns where it ends.
func (tm *program) waitFor(what string, from int, ok func(string) bool) int {
	tm.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		tm.mu.Lock()
		got, end := string(tm.got[from:]), len(tm.got)
		tm.mu.Unlock()
		if ok(got) {
			return end
		}
		select {
		case <-tm.grew:
		case <-deadline:
			tm.t.Fatalf("never wrote %s; wrote %q", what, got)
		}
	}
}

// frameWith waits for a whole frame from byte from on that holds text,
// any with "", and returns where it ends.
func (tm *program) frameWith(text string, from int) int {
	tm.t.Helper()
	return tm.waitFor(text, from, func(s string) bool {
		i := strings.Index(s, text)
		return i >= 0 && strings.Contains(s[i:], "\x1b[?2026l")
	})
}

// do runs fn on the UI thread, and the Update that follows it.
func (tm *program) do(fn func()) {
	tm.t.Helper()
	done := make(chan struct{})
	tm.h.m.posts.post(func() {
		fn()
		close(done)
	})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		tm.t.Fatal("the UI thread never ran")
	}
}

// waitLocked waits until a goroutine waits for a mutex in fn, as the
// runtime's stacks show: there is no other way to see Bubble Tea's
// renderer wait.
func waitLocked(t *testing.T, fn string) {
	t.Helper()
	buf := make([]byte, 1<<20)
	deadline := time.Now().Add(5 * time.Second)
	for {
		n := runtime.Stack(buf, true)
		for g := range strings.SplitSeq(string(buf[:n]), "\n\n") {
			if strings.Contains(g, "[sync.Mutex.Lock") && strings.Contains(g, fn) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("nothing waits in %s", fn)
		}
		time.Sleep(time.Millisecond)
	}
}

// replay reads what an 80×24 terminal was written and checks each frame:
// one that wrote, erased or moved a cell of covers drew the cover's image
// at its corner, one that left them did not, and none wrote after an
// image.
func replay(t *testing.T, written string, covers ...image.Rectangle) *vt {
	t.Helper()
	v := newVT(80, 24)
	if err := v.write(written); err != nil {
		t.Fatal(err)
	}
	for i, f := range v.frames {
		if f.wroteAfter {
			t.Errorf("frame %d wrote after an image:\n%q", i, f.text)
		}
		for _, r := range covers {
			drew := slices.ContainsFunc(f.images, func(img vtImage) bool { return img.at == r.Min })
			switch wrote := f.wrote(r); {
			case wrote && !drew && f.showsOverlay(r):
				t.Errorf("frame %d wrote over the cover at %v, and drew it not:\n%q", i, r, f.text)
			case drew && !wrote:
				t.Errorf("frame %d drew the cover at %v again over cells it left:\n%q", i, r, f.text)
			}
		}
	}
	return v
}

// showsOverlay reports r's cells an overlay's blank once f was written.
func (f vtFrame) showsOverlay(r image.Rectangle) bool {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for _, c := range f.cells[y][r.Min.X:r.Max.X] {
			if c != "\u2800" {
				return false
			}
		}
	}
	return true
}

// wrote reports f writing, erasing or moving a cell of r.
func (f vtFrame) wrote(r image.Rectangle) bool {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		if slices.Contains(f.touched[y][r.Min.X:r.Max.X], true) {
			return true
		}
	}
	return false
}

// barCover is where the bar's cover shows on an 80×24 screen.
var barCover = image.Rect(1, 21, 5, 23)

// TestTermWideLine draws the cover again after each tick of a song whose
// title is wide: the renderer repaints its line whole every frame.
func TestTermWideLine(t *testing.T) {
	h := placeholderHarness(t)
	h.withCovers(termimage.Sixel, newCoverHost(t))
	song := h.mirror.songs[h.mirror.state.Current]
	song.Title = "夜明けのアーケード"
	h.mirror.songs[song.ID] = song
	h.mirror.state.Playing = true
	tm := runTerm(t, h)
	at := tm.frameWith("\x1bP", 0)
	for range 3 {
		tm.do(func() { h.clock = h.clock.Add(time.Second) })
		at = tm.frameWith("", at)
	}
	v := replay(t, tm.quit(), barCover)
	if got := v.screen(); got != h.screen() {
		t.Errorf("the terminal shows\n%s\nwant\n%s", got, h.screen())
	}
	ticks := 0
	for _, f := range v.frames {
		if strings.Contains(f.text, "夜明け") && len(f.images) == 1 {
			ticks++
		}
	}
	if ticks < 4 {
		t.Errorf("%d frames wrote the title and drew the cover, want the first and 3 ticks'", ticks)
	}
}

// TestTermCursor shows the terminal's cursor on the search input's cell
// while typing, after the covers drawn: the bar's wide line repaints, and
// its cover is drawn, every frame.
func TestTermCursor(t *testing.T) {
	h := placeholderHarness(t)
	h.withCovers(termimage.Sixel, newCoverHost(t))
	song := h.mirror.songs[h.mirror.state.Current]
	song.Title = "夜明けのアーケード"
	h.mirror.songs[song.ID] = song
	h.mirror.state.Playing = true
	tm := runTerm(t, h)
	at := tm.frameWith("\x1bP", 0)
	tm.p.Send(keyMsg("/"))
	at = tm.frameWith(ansi.SetModeTextCursorEnable, at)
	for _, r := range "月の" {
		tm.p.Send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	at = tm.frameWith("の", at)
	tm.do(func() { h.clock = h.clock.Add(time.Second) })
	at = tm.frameWith("", at)
	tm.p.Send(keyMsg("esc"))
	tm.frameWith(ansi.ResetModeTextCursorEnable, at)
	v := replay(t, tm.quit(), barCover)

	input := func(x int) image.Point { return image.Pt(x, tabsRows) }
	var shows, typed, hides int
	for i, f := range v.frames {
		switch {
		case strings.Contains(f.text, ansi.SetModeTextCursorEnable):
			shows++
			overlay := strings.Index(f.text, "\x1b7\x1b[22;2H")
			if overlay < 0 || overlay > strings.Index(f.text, ansi.SetModeTextCursorEnable) {
				t.Errorf("frame %d showed the cursor before drawing the cover:\n%q", i, f.text)
			}
			if !f.isCursorShown || f.cursor != input(3) {
				t.Errorf("frame %d left the cursor at %v, shown %t; want %v", i, f.cursor, f.isCursorShown, input(3))
			}
		case shows == 0:
		case strings.Contains(f.text, ansi.ResetModeTextCursorEnable):
			hides++
			if f.isCursorShown {
				t.Errorf("frame %d, done typing, shows the cursor", i)
			}
		case hides == 0 && strings.Contains(strings.Join(f.cells[tabsRows], ""), "月の"):
			typed++
			if !f.isCursorShown || f.cursor != input(7) || len(f.images) != 1 {
				t.Errorf("frame %d left the cursor at %v, shown %t, drew %d covers; want %v, the cover",
					i, f.cursor, f.isCursorShown, len(f.images), input(7))
			}
		}
	}
	if shows != 1 || typed < 2 || hides != 1 {
		t.Errorf("%d frames showed the cursor, %d typed, %d hid it; want 1, the query's and the tick's, 1", shows, typed, hides)
	}
}

// TestTermCoverPaused shows a cover encoded while the song is paused:
// nothing else changes, yet a frame writes its marks.
func TestTermCoverPaused(t *testing.T) {
	h := placeholderHarness(t)
	host := newCoverHost(t)
	host.hold = make(chan struct{})
	h.withCovers(termimage.Sixel, host)
	tm := runTerm(t, h)
	if got := tm.written(0); strings.Contains(got, "\x1bP") {
		t.Fatal("drew a cover not loaded")
	}
	close(host.hold)
	tm.frameWith("\x1b7\x1b[22;2H\x1bP", 0)
	replay(t, tm.quit(), barCover)
}

// TestTermBlockedWriter writes, after Update published generation N, the
// frame of N-1 the renderer took before, stuck behind a slow write:
// that frame draws N-1's cover, not N's. Updates meanwhile queue behind
// the renderer, which holds its lock until it wrote: no frame's table is
// gone (runTerm's missing), the bound the two tables rely on.
func TestTermBlockedWriter(t *testing.T) {
	const blueURL = "https://lh3.googleusercontent.com/blue=w120-h120"
	h := placeholderHarness(t)
	host := newCoverHost(t)
	host.pngs = map[string][]byte{"/blue": colorPNG(t, color.RGBA{30, 40, 220, 255})}
	h.withCovers(termimage.Sixel, host)
	red := h.mirror.state.Current
	blue := h.mirror.songs[h.mirror.queue[1]]
	blue.Art = blueURL
	h.mirror.songs[blue.ID] = blue
	play := func(id string) {
		h.mirror.state.Current = id
		h.mirror.gen++
	}
	// Both covers encoded, so that either shows at once.
	h.covered()
	play(blue.ID)
	h.covered()
	play(red)
	h.covered()
	var redSeq, blueSeq string
	for _, c := range h.m.covers.cache {
		switch c.key.url {
		case coverURL:
			redSeq = c.Overlay
		case blueURL:
			blueSeq = c.Overlay
		}
	}
	if redSeq == "" || blueSeq == "" || redSeq == blueSeq {
		t.Fatal("the covers are not encoded apart")
	}

	tm := runTerm(t, h)
	at := tm.frameWith(redSeq, 0)
	// A write holds termOut, as Bubble Tea's event loop may while the
	// renderer writes the frame of N-1.
	tm.out.mu.Lock()
	unlock := sync.OnceFunc(tm.out.mu.Unlock)
	defer unlock()
	tm.do(func() { play(blue.ID) })
	waitLocked(t, "(*termOut).Write")
	tm.do(func() { play(red) })
	waitLocked(t, "(*cursedRenderer).render")
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		for _, id := range []string{blue.ID, red, blue.ID, red} {
			h.m.posts.post(func() { play(id) })
			tm.p.Send(postedMsg{})
		}
	}()
	unlock()
	<-sent
	all := tm.quit()
	replay(t, all, barCover)
	rest := all[at:]
	if b, r := strings.Index(rest, blueSeq), strings.Index(rest, redSeq); b < 0 || r < b {
		t.Errorf("after N, drew red at %d and blue at %d, want N-1's blue first", r, b)
	}
}

// TestTermIdleTicks sends no cover again while nothing wipes it, in one
// generation: ticks while paused write nothing, and those while playing a
// song of ASCII write the progress beside the cover, not over it.
func TestTermIdleTicks(t *testing.T) {
	h := placeholderHarness(t)
	h.withCovers(termimage.Sixel, newCoverHost(t))
	h.covered()
	gen := h.m.covers.published
	tm := runTerm(t, h)
	at := tm.frameWith("\x1bP", 0)
	for range 5 {
		tm.p.Send(tickMsg{})
	}
	tm.do(func() { h.m.say("Paused") })
	at = tm.frameWith("Paused", at)
	tm.do(func() {
		h.mirror.state.Playing = true
		h.mirror.gen++
	})
	at = tm.frameWith("", at)
	for range 3 {
		tm.do(func() { h.clock = h.clock.Add(time.Second) })
		at = tm.frameWith("", at)
	}
	tm.do(func() {
		if h.m.covers.published != gen {
			t.Errorf("generation %d, want %d", h.m.covers.published, gen)
		}
	})
	v := replay(t, tm.quit(), barCover)
	drew := 0
	for _, f := range v.frames {
		drew += len(f.images)
	}
	if len(v.frames) < 6 || drew != 1 {
		t.Errorf("%d frames drew %d covers, want only the first", len(v.frames), drew)
	}
}

// TestTermResize draws covers where they fit the screen, above its last
// line, as it shrinks with them at its bottom: the frames of the last
// view at the new size too.
func TestTermResize(t *testing.T) {
	h := newHarness(t, albumArt{catalog.NewPlaceholderSource(0)})
	h.withCovers(termimage.Sixel, newCoverHost(t))
	h.m.open(opened{kind: itemAlbum, id: catalog.Placeholder().Albums[0].ID})
	h.covered()
	tm := runTerm(t, h)
	at := max(tm.frameWith("\x1b7\x1b[22;2H\x1bP", 0), tm.frameWith("\x1b7\x1b[3;2H\x1bP", 0))
	for _, tc := range []struct {
		w, h int
		// want are the overlays' cells, 1-based, the last frame draws.
		want []string
		// done is a notice after the resize, none of whose letters the
		// last's has where it has them.
		done string
	}{
		{80, 4, []string{"2;2"}, "ab1"},
		{80, 3, []string{"1;2"}, "cd2"},
		{80, 2, nil, "ef3"},
		{44, 24, []string{"3;2", "22;2"}, "gh4"},
	} {
		tm.p.Send(tea.WindowSizeMsg{Width: tc.w, Height: tc.h})
		tm.do(func() { h.m.say(tc.done) })
		end := tm.frameWith(tc.done, at)
		for _, want := range tc.want {
			end = max(end, tm.frameWith("\x1b7\x1b["+want+"H\x1bP", at))
		}
		for _, p := range overlaysAt(tm.written(at)[:end-at]) {
			if p.X+4 > tc.w || p.Y+2 > tc.h-1 {
				t.Errorf("%d×%d: drew a cover at %v", tc.w, tc.h, p)
			}
		}
		at = end
	}
}

// overlaysAt are the cells, from 0, of the overlays in s.
func overlaysAt(s string) []image.Point {
	var at []image.Point
	for _, m := range overlayAt.FindAllStringSubmatch(s, -1) {
		y, _ := strconv.Atoi(m[1])
		x, _ := strconv.Atoi(m[2])
		at = append(at, image.Pt(x-1, y-1))
	}
	return at
}

var overlayAt = regexp.MustCompile(`\x1b7\x1b\[(\d+);(\d+)H`)

// TestTermQuit leaves no cover on the main screen: the last frame draws
// it on the alternate screen, which leaving drops.
func TestTermQuit(t *testing.T) {
	h := placeholderHarness(t)
	h.withCovers(termimage.Sixel, newCoverHost(t))
	song := h.mirror.songs[h.mirror.state.Current]
	song.Title = "夜明けのアーケード"
	h.mirror.songs[song.ID] = song
	h.covered()
	tm := runTerm(t, h)
	tm.frameWith("\x1bP", 0)
	all := tm.quit()
	_, after, ok := strings.Cut(all[strings.LastIndex(all, "\x1b[?2026h"):], "\x1b[?1049l")
	if !ok || strings.Contains(after, "\x1b7") {
		t.Errorf("after the alternate screen, wrote %q", after)
	}
	if v := replay(t, all, barCover); v.mainImages != 0 {
		t.Errorf("drew %d covers on the main screen", v.mainImages)
	}
}

// longAlbum is albumArt with its albums' songs 30 times over, each copy a
// song of its own but the first: a list that scrolls.
type longAlbum struct{ albumArt }

func (s longAlbum) Album(ctx context.Context, id string) (catalog.Album, []catalog.Song, error) {
	a, songs, err := s.albumArt.Album(ctx, id)
	long := slices.Clone(songs)
	for i := 1; i < 30; i++ {
		for _, song := range songs {
			song.ID += "-" + strconv.Itoa(i)
			long = append(long, song)
		}
	}
	return a, long, err
}

// headerCover is where an album's cover shows on an 80×24 screen.
var headerCover = image.Rect(1, 2, 5, 4)

// TestTermASCIILines holds the marks' premise where the renderer diffs
// lines rather than repaints them, those without wide characters: a frame
// that wrote over a cover wrote its mark, whatever it changed beside it.
// The album's list scrolls under its cover; the bar's song, its progress
// and its like change beside the bar's.
func TestTermASCIILines(t *testing.T) {
	h := newHarness(t, longAlbum{albumArt{catalog.NewPlaceholderSource(0)}})
	h.withCovers(termimage.Sixel, newCoverHost(t))
	for i, title := range []string{"A", strings.Repeat("W", 70), "x   y   z", strings.Repeat("a", 40)} {
		song := h.mirror.songs[h.mirror.queue[i+1]]
		song.Title, song.Art, song.Seconds = title, coverURL, 59+i*1000
		h.mirror.songs[song.ID] = song
	}
	h.m.open(opened{kind: itemAlbum, id: catalog.Placeholder().Albums[0].ID})
	h.loaded()
	h.covered()
	tm := runTerm(t, h)
	at := max(tm.frameWith("\x1b7\x1b[22;2H\x1bP", 0), tm.frameWith("\x1b7\x1b[3;2H\x1bP", 0))
	keys := func(keys ...string) {
		for _, k := range keys {
			tm.p.Send(keyMsg(k))
		}
		at = tm.frameWith("", at)
	}
	keys("down", "down", "down")
	keys(slices.Repeat([]string{"down"}, 15)...)
	keys("pgdown")
	keys("end")
	keys("up", "up", "up", "up", "up")
	keys("pgup", "pgup")
	keys("home", "down")
	keys("l")
	for i := range 4 {
		tm.do(func() {
			h.mirror.state.Current = h.mirror.queue[i+1]
			h.mirror.state.Duration = float64(h.mirror.songs[h.mirror.state.Current].Seconds)
			h.mirror.gen++
			h.clock = h.clock.Add(time.Duration(i*400) * time.Second)
		})
		at = tm.frameWith("", at)
	}
	v := replay(t, tm.quit(), headerCover, barCover)
	beside := 0
	for _, f := range v.frames {
		if !f.wrote(headerCover) && !f.wrote(barCover) && f.wrote(image.Rect(5, 4, 80, 23)) {
			beside++
		}
	}
	if beside < 8 {
		t.Errorf("%d frames wrote beside the covers, not over them; want most", beside)
	}
}

// TestTermWidthMethod replays emoji titles on a terminal that measures
// as the renderer was told to: by rune, or by grapheme once it answered
// mode 2027. Every line fits the screen, the songs' lengths line up.
func TestTermWidthMethod(t *testing.T) {
	for _, tc := range []struct {
		name, answers string
		method        ansi.Method
	}{
		{"wcwidth", "", ansi.WcWidth},
		{"graphemes", "\x1b[?2027;2$y", ansi.GraphemeWidth},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := placeholderHarness(t)
			songs := append(slices.Clone(emojiSongs), wideSongs...)
			h.mirror.setQueue(songs)
			h.mirror.state.Playing = true
			h.send(postedMsg{})
			h.press("down", "down")
			written := runTermAnswering(t, h, tc.answers).quit()
			// The renderer sets mode 2027 where it measures graphemes.
			setsMode := strings.Contains(written, ansi.SetModeUnicodeCore)
			if h.m.cells.method != tc.method || setsMode != (tc.method == ansi.GraphemeWidth) {
				t.Fatalf("measured by %v, mode 2027 set: %t; want %v", h.m.cells.method, setsMode, tc.method)
			}
			v := replay(t, written)
			if got := v.screen(); got != h.screen() {
				t.Errorf("the terminal shows\n%s\nwant\n%s", got, h.screen())
			}
			var cols []int
			for _, row := range v.cells[tabsRows : tabsRows+len(songs)] {
				colon := -1
				for x, c := range row {
					if c == ":" {
						colon = x
					}
				}
				cols = append(cols, colon)
			}
			if slices.ContainsFunc(cols, func(x int) bool { return x != cols[0] }) {
				t.Errorf("songs' lengths end at cells %v:\n%s", cols, v.screen())
			}
		})
	}
}
