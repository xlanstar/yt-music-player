package tui

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/fetchstate"
	"yt-music-player/internal/loader"
	"yt-music-player/internal/remote"
	"yt-music-player/internal/session"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/egoist/mygo/i18n"
	"github.com/rs/zerolog"
)

var update = flag.Bool("update", false, "rewrite the golden screens in testdata/")

func TestMain(m *testing.M) {
	// Only main sets the level: tests would otherwise print every failure
	// they provoke.
	zerolog.SetGlobalLevel(zerolog.Disabled)
	os.Exit(m.Run())
}

// epoch is the tests' clock: positions stand still.
var epoch = time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)

// fakeCtl records the controls sent, as "Method args".
type fakeCtl struct{ calls []string }

var _ remote.Controller = (*fakeCtl)(nil)

func (c *fakeCtl) call(format string, args ...any) {
	c.calls = append(c.calls, fmt.Sprintf(format, args...))
}

func (c *fakeCtl) Play(ids []string, start int, _ []catalog.Song) {
	c.call("Play %s %d", strings.Join(ids, ","), start)
}

func (c *fakeCtl) PlayAll(ids []string, _ []catalog.Song) {
	c.call("PlayAll %s", strings.Join(ids, ","))
}
func (c *fakeCtl) Toggle()                  { c.call("Toggle") }
func (c *fakeCtl) Pause()                   { c.call("Pause") }
func (c *fakeCtl) Resume()                  { c.call("Resume") }
func (c *fakeCtl) Next()                    { c.call("Next") }
func (c *fakeCtl) Prev()                    { c.call("Prev") }
func (c *fakeCtl) Seek(seconds float64)     { c.call("Seek %g", seconds) }
func (c *fakeCtl) SetVolume(volume float64) { c.call("SetVolume %.2f", volume) }
func (c *fakeCtl) ToggleMute()              { c.call("ToggleMute") }
func (c *fakeCtl) CycleRepeat()             { c.call("CycleRepeat") }
func (c *fakeCtl) ToggleShuffle()           { c.call("ToggleShuffle") }

func (c *fakeCtl) Enqueue(ids []string, next bool, _ []catalog.Song) {
	c.call("Enqueue %s %t", strings.Join(ids, ","), next)
}
func (c *fakeCtl) Remove(index int)  { c.call("Remove %d", index) }
func (c *fakeCtl) Move(from, to int) { c.call("Move %d %d", from, to) }
func (c *fakeCtl) ClearUpNext()      { c.call("ClearUpNext") }

func (c *fakeCtl) take() []string {
	calls := c.calls
	c.calls = nil
	return calls
}

// fakeMirror is playback as a test sets it; its state arrived at epoch.
type fakeMirror struct {
	state api.State
	// gen is StateGen: a test that sets a new State moves it on.
	gen   uint64
	queue []string
	songs map[string]catalog.Song
}

func (f *fakeMirror) State() api.State { return f.state }
func (f *fakeMirror) StateGen() uint64 { return f.gen }
func (f *fakeMirror) Position(now time.Time) float64 {
	return remote.Position(f.state, epoch, now)
}
func (f *fakeMirror) Queue() []string { return f.queue }
func (f *fakeMirror) Song(id string) (catalog.Song, bool) {
	s, ok := f.songs[id]
	return s, ok
}

// setQueue queues songs, the first current, as paused at 41 s.
func (f *fakeMirror) setQueue(songs []catalog.Song) {
	f.queue = make([]string, len(songs))
	f.songs = make(map[string]catalog.Song, len(songs))
	for i, s := range songs {
		f.queue[i] = s.ID
		f.songs[s.ID] = s
	}
	f.state = api.State{Volume: 0.4, Repeat: api.RepeatOff}
	if len(songs) > 0 {
		f.state.Current, f.state.Position, f.state.Duration = songs[0].ID, 41, float64(songs[0].Seconds)
	}
}

// fakeSup records what the TUI asked of the Supervisor.
type fakeSup struct{ calls []string }

func (s *fakeSup) Retry()      { s.calls = append(s.calls, "Retry") }
func (s *fakeSup) Restart()    { s.calls = append(s.calls, "Restart") }
func (s *fakeSup) Foreground() { s.calls = append(s.calls, "Foreground") }

// fakeDaemon is the session's daemon; it records requests and answers
// none, so likes stay pending.
type fakeDaemon struct{ calls []string }

var _ session.Daemon = (*fakeDaemon)(nil)

func (d *fakeDaemon) CanSignIn() bool { return true }
func (d *fakeDaemon) Refresh(part api.Part, _ func(error)) {
	d.calls = append(d.calls, "Refresh "+strconv.Itoa(int(part)))
}

func (d *fakeDaemon) Like(id string, liked bool, _ *catalog.Song, _ func(error)) {
	d.calls = append(d.calls, fmt.Sprintf("Like %s %t", id, liked))
}

func (d *fakeDaemon) SignIn(string, func(catalog.Account, error)) {
	d.calls = append(d.calls, "SignIn")
}
func (d *fakeDaemon) CancelSignIn()       {}
func (d *fakeDaemon) SignOut(func(error)) { d.calls = append(d.calls, "SignOut") }

// harness is a model over fakes and a Source, on the test's goroutine,
// its UI thread; it renders without colors.
type harness struct {
	t      testing.TB
	m      *model
	ctl    *fakeCtl
	mirror *fakeMirror
	sup    *fakeSup
	daemon *fakeDaemon
	opened int
	// clock is the model's now: epoch, unless a test moves it on.
	clock time.Time
}

// newHarness shows the placeholder's queue, paused, on an 80×24
// terminal, connected, with no one signed in yet.
func newHarness(t testing.TB, src catalog.Source) *harness {
	t.Helper()
	posts := newPoster()
	states := &fetchstate.Registry{}
	cat := catalog.New(nil, nil, nil)
	load := loader.New(src, states, cat, posts.post)
	load.Debounce = 0
	h := &harness{t: t, ctl: &fakeCtl{}, mirror: &fakeMirror{}, sup: &fakeSup{}, daemon: &fakeDaemon{}, clock: epoch}
	h.mirror.setQueue(catalog.Placeholder().Songs)
	h.m = newModel(deps{
		ctl:     h.ctl,
		mirror:  h.mirror,
		sup:     h.sup,
		session: session.New(h.daemon, states, load, cat),
		loader:  load,
		cat:     cat,
		states:  states,
		posts:   posts,
		now:     func() time.Time { return h.clock },
		openDesktop: func() error {
			h.opened++
			return nil
		},
		daemonLog: "/logs/ytmd.log",
	})
	h.m.link = remote.Link{Health: remote.Connected, Connection: 1}
	t.Cleanup(load.Stop)
	h.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	return h
}

// placeholderHarness is newHarness over the placeholder's source, its
// user signed in with their library and likes.
func placeholderHarness(t testing.TB) *harness {
	t.Helper()
	src := catalog.NewPlaceholderSource(0)
	h := newHarness(t, src)
	user, err := src.Account(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lib, err := src.Library(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s := h.m.session
	s.KeepAccount(user, lib)
	s.KeepLikes(catalog.PlaceholderLikes())
	h.send(postedMsg{})
	return h
}

// send updates the model with msg and returns its command.
func (h *harness) send(msg tea.Msg) tea.Cmd {
	h.t.Helper()
	_, cmd := h.m.Update(msg)
	return cmd
}

// press presses keys, named as Bubble Tea names them ("enter", "up",
// "shift+tab", "ctrl+c") or as typed ("a", "R", "/").
func (h *harness) press(keys ...string) tea.Cmd {
	h.t.Helper()
	var cmd tea.Cmd
	for _, k := range keys {
		cmd = h.send(keyMsg(k))
	}
	return cmd
}

// typeText types s, a key per rune.
func (h *harness) typeText(s string) {
	h.t.Helper()
	for _, r := range s {
		h.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

var namedKeys = map[string]tea.Key{
	"enter":     {Code: tea.KeyEnter},
	"tab":       {Code: tea.KeyTab},
	"shift+tab": {Code: tea.KeyTab, Mod: tea.ModShift},
	"esc":       {Code: tea.KeyEscape},
	"backspace": {Code: tea.KeyBackspace},
	"up":        {Code: tea.KeyUp},
	"down":      {Code: tea.KeyDown},
	"left":      {Code: tea.KeyLeft},
	"right":     {Code: tea.KeyRight},
	"home":      {Code: tea.KeyHome},
	"end":       {Code: tea.KeyEnd},
	"space":     {Code: tea.KeySpace, Text: " "},
	"ctrl+c":    {Code: 'c', Mod: tea.ModCtrl},
	"ctrl+u":    {Code: 'u', Mod: tea.ModCtrl},
}

func keyMsg(k string) tea.KeyPressMsg {
	if key, ok := namedKeys[k]; ok {
		return tea.KeyPressMsg(key)
	}
	r := []rune(k)
	if len(r) != 1 {
		panic("no key " + k)
	}
	return tea.KeyPressMsg{Code: r[0], Text: k}
}

// settle runs what the loader and the session post until done holds.
func (h *harness) settle(what string, done func() bool) {
	h.t.Helper()
	deadline := time.After(5 * time.Second)
	for !done() {
		select {
		case <-h.m.posts.wake:
			h.send(postedMsg{})
		case <-deadline:
			h.t.Fatalf("never %s:\n%s", what, h.screen())
		}
	}
}

// loaded settles until the loader is idle.
func (h *harness) loaded() {
	h.t.Helper()
	h.settle("loaded", func() bool { return !h.m.loader.IsBusy() })
}

// resize shows the screen on a terminal of w×ht cells.
func (h *harness) resize(w, ht int) { h.send(tea.WindowSizeMsg{Width: w, Height: ht}) }

// screen is the screen without styles, its lines' trailing spaces cut:
// what a terminal without colors shows.
func (h *harness) screen() string {
	plain := ansi.Strip(h.m.View().Content)
	lines := strings.Split(plain, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n") + "\n"
}

// golden compares the screen with testdata/name.golden; -update writes it.
func (h *harness) golden(name string) {
	h.t.Helper()
	got := h.screen()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			h.t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			h.t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		h.t.Fatalf("%v (run with -update)", err)
	}
	if got != string(want) {
		h.t.Errorf("screen %s differs (-update rewrites it)\ngot:\n%s\nwant:\n%s", name, got, want)
	}
}

// screenHas fails unless the screen shows each of texts.
func (h *harness) screenHas(texts ...string) {
	h.t.Helper()
	s := h.screen()
	for _, text := range texts {
		if !strings.Contains(s, text) {
			h.t.Errorf("the screen lacks %q:\n%s", text, s)
		}
	}
}

// inLanguage shows messages in lang until the test ends; make the
// harness after.
func inLanguage(t *testing.T, lang string) {
	t.Helper()
	i18n.SetLocaleFunc(func() string { return lang })
	t.Cleanup(func() { i18n.SetLocaleFunc(func() string { return "" }) })
}
