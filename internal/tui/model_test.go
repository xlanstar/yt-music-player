package tui

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/notice"
	"yt-music-player/internal/remote"

	tea "charm.land/bubbletea/v2"
)

func TestQueue(t *testing.T) {
	h := placeholderHarness(t)
	h.golden("queue")

	h.mirror.state.Playing, h.mirror.state.Shuffled, h.mirror.state.Repeat = true, true, api.RepeatAll
	h.send(postedMsg{})
	h.screenHas("▶ Midnight Arcade", "Shuffle on · Repeat all · Volume 40%")

	h.resize(44, 14)
	h.golden("queue-narrow")
}

func TestQueueScrolls(t *testing.T) {
	h := placeholderHarness(t)
	songs := longQueue(300)
	h.mirror.setQueue(songs)
	h.mirror.state.Index, h.mirror.state.Current = 150, songs[150].ID
	h.send(postedMsg{})
	// The cursor follows the current song until moved.
	h.golden("queue-follows")
	h.press("end")
	h.golden("queue-end")
	h.press("home", "down", "down")
	h.press("enter")
	if got := h.ctl.take(); !reflect.DeepEqual(got, []string{"Play song2 0"}) {
		t.Errorf("enter sent %q", got)
	}
}

func TestEmptyQueue(t *testing.T) {
	h := newHarness(t, catalog.NewPlaceholderSource(0))
	h.mirror.setQueue(nil)
	h.send(postedMsg{})
	h.golden("queue-empty")
	if cmd := h.press("left"); cmd != nil || len(h.ctl.calls) > 0 {
		t.Errorf("seeking nothing sent %q", h.ctl.calls)
	}
}

func TestPlaybackKeys(t *testing.T) {
	cases := []struct {
		key  string
		want string
	}{
		{"space", "Toggle"},
		{"n", "Next"},
		{"p", "Prev"},
		{"left", "Seek 36"},
		{"right", "Seek 46"},
		{"+", "SetVolume 0.45"},
		{"-", "SetVolume 0.35"},
		{"s", "ToggleShuffle"},
		{"r", "CycleRepeat"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			h := placeholderHarness(t)
			h.press(tc.key)
			if got := h.ctl.take(); !reflect.DeepEqual(got, []string{tc.want}) {
				t.Errorf("%s sent %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}

func TestVolumeStops(t *testing.T) {
	h := placeholderHarness(t)
	h.mirror.state.Volume = 0.98
	h.press("+")
	h.mirror.state.Volume = 0.02
	h.press("-")
	if got, want := h.ctl.take(), []string{"SetVolume 1.00", "SetVolume 0.00"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sent %q, want %q", got, want)
	}
}

func TestQuit(t *testing.T) {
	for _, key := range []string{"q", "ctrl+c"} {
		h := placeholderHarness(t)
		cmd := h.press(key)
		if !isQuit(cmd) {
			t.Errorf("%s did not quit", key)
		}
		if len(h.ctl.calls) > 0 || len(h.sup.calls) > 0 {
			t.Errorf("%s also sent %q %q: quitting must leave the daemon playing", key, h.ctl.calls, h.sup.calls)
		}
	}
}

// isQuit reports whether cmd, maybe in a batch, quits.
func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	switch msg := cmd().(type) {
	case tea.QuitMsg:
		return true
	case tea.BatchMsg:
		return slices.ContainsFunc(msg, isQuit)
	}
	return false
}

// Windows sends a lone Ctrl as an empty key, which must change nothing.
func TestEmptyKey(t *testing.T) {
	h := placeholderHarness(t)
	before := h.m.content
	h.m.content = ""
	if cmd := h.send(tea.KeyPressMsg{}); cmd != nil {
		t.Error("an empty key gave a command")
	}
	if h.m.content != "" {
		t.Error("an empty key rendered the screen")
	}
	h.m.content = before
}

func TestSearch(t *testing.T) {
	h := placeholderHarness(t)
	h.press("tab")
	h.golden("search-idle")
	h.press("/")
	// Keys go to the input while it has focus: q types, not quits.
	h.typeText("moq")
	h.press("backspace")
	h.typeText("on")
	if h.m.query != "moon" || len(h.ctl.calls) > 0 {
		t.Fatalf("typed %q, sent %q", h.m.query, h.ctl.calls)
	}
	h.loaded()
	h.golden("search")
	h.press("esc", "down", "enter")
	if got := h.ctl.take(); !reflect.DeepEqual(got, []string{"Play slow-burn-summer 0"}) {
		t.Errorf("enter sent %q", got)
	}
	// An album found opens over the results; esc goes back to them.
	h.press("down", "enter")
	h.loaded()
	h.golden("search-album")
	h.press("esc")
	h.screenHas("/ moon", "Slow Burn Summer")
	if isQuit(h.press("q")) == false {
		t.Error("q did not quit once the input lost focus")
	}
}

func TestSearchNone(t *testing.T) {
	h := placeholderHarness(t)
	h.press("/")
	h.typeText("zzz")
	h.loaded()
	h.screenHas(`No results for “zzz”`)
	h.press("ctrl+u")
	h.screenHas("Press / and type")
}

// failing is the placeholder's source, failing what tests ask it to.
type failing struct {
	*catalog.PlaceholderSource
	err error
	// calls counts the requests, made on the loader's goroutines.
	calls atomic.Int32
}

func (f *failing) Search(context.Context, string) (catalog.Results, error) {
	f.calls.Add(1)
	return catalog.Results{}, f.err
}

func (f *failing) Album(context.Context, string) (catalog.Album, []catalog.Song, error) {
	f.calls.Add(1)
	return catalog.Album{}, nil, f.err
}

func TestSearchFailed(t *testing.T) {
	src := &failing{PlaceholderSource: catalog.NewPlaceholderSource(0), err: errors.New("the network is down")}
	h := newHarness(t, src)
	h.press("/")
	h.typeText("moon")
	h.loaded()
	h.press("enter")
	h.screenHas("/ moon", "Couldn’t search for “moon”", "the network is down", "enter Retry")
	calls := src.calls.Load()
	h.press("enter")
	h.loaded()
	if got := src.calls.Load(); got != calls+1 {
		t.Errorf("enter did not retry: %d searches, want %d", got, calls+1)
	}
}

func TestLibrary(t *testing.T) {
	h := placeholderHarness(t)
	h.press("shift+tab")
	h.golden("library")

	h.press("enter")
	h.golden("liked")
	h.press("enter")
	if got := h.ctl.take(); len(got) != 1 || !strings.HasPrefix(got[0], "PlayAll ") {
		t.Errorf("Play all sent %q", got)
	}
	h.press("esc", "down", "enter")
	h.loaded()
	h.golden("playlist")

	// The daemon skips songs queued already, which the placeholder's
	// queue holds; a says so.
	h.press("down", "a")
	h.screenHas(`“Midnight Arcade” is already in the queue`)
	h.press("up", "a")
	h.screenHas("These songs are already in the queue")
	h.mirror.queue = nil
	h.press("down", "a")
	h.screenHas(`Added “Midnight Arcade” to the queue`)
	h.mirror.queue = []string{"midnight-arcade"}
	h.press("up", "a")
	h.screenHas("Added 6 songs to the queue")
	if got := h.ctl.take(); len(got) != 4 || got[0] != "Enqueue midnight-arcade false" {
		t.Errorf("a sent %q", got)
	}

	// l likes the selected song at once, through the session.
	h.press("down", "down", "down", "down", "down", "l")
	if got := h.daemon.calls; !slices.Contains(got, "Like satellite-hearts true") {
		t.Errorf("l asked %q", got)
	}
	if !h.m.session.Liked("satellite-hearts") {
		t.Error("the like does not show")
	}
}

func TestLibraryStates(t *testing.T) {
	h := newHarness(t, catalog.NewPlaceholderSource(0))
	h.press("shift+tab")
	h.screenHas("Loading…")

	h.m.session.ApplyFailed(api.FailedEvent{Part: api.PartLibrary, Err: &api.Error{Code: api.CodeInternal}})
	h.send(postedMsg{})
	h.golden("library-failed")
	h.press("enter")
	if !slices.Contains(h.daemon.calls, "Refresh 2") {
		t.Errorf("enter asked %q, not to refresh the library", h.daemon.calls)
	}
}

func TestSignedOut(t *testing.T) {
	h := placeholderHarness(t)
	h.m.session.ApplyAccount(api.AccountEvent{SignedIn: false})
	h.press("shift+tab")
	h.golden("signed-out")

	cmd := h.press("o")
	if cmd == nil {
		t.Fatal("o did nothing")
	}
	h.send(runCmd(cmd))
	if h.opened != 1 {
		t.Errorf("opened the Desktop %d times", h.opened)
	}
	h.m.openDesktop = func() error { return errors.New("no such app") }
	h.send(runCmd(h.press("o")))
	h.screenHas("Couldn’t open the Desktop app: no such app")

	// Liking asks to sign in.
	h.press("tab", "l")
	h.screenHas("Sign in to like songs")
}

// runCmd runs cmd, maybe a batch, and returns the message it makes that
// is not a timer's.
func runCmd(cmd tea.Cmd) tea.Msg {
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c == nil {
				continue
			}
			if m, ok := c().(openedDesktopMsg); ok {
				return m
			}
		}
	}
	return msg
}

func TestSignedInOnlyOpens(t *testing.T) {
	h := placeholderHarness(t)
	if cmd := h.press("o"); cmd != nil || h.opened != 0 {
		t.Error("o opened the Desktop for a signed-in user")
	}
}

func TestCollectionStates(t *testing.T) {
	src := &failing{PlaceholderSource: catalog.NewPlaceholderSource(0), err: errors.New("the network is down")}
	h := newHarness(t, src)
	h.press("shift+tab")
	h.m.open(opened{kind: itemAlbum, id: "dead-air"})
	h.send(postedMsg{})
	h.loaded()
	h.screenHas("Couldn’t load this album", "the network is down", "enter Retry")
	h.press("enter")
	h.loaded()
	if got := src.calls.Load(); got != 2 {
		t.Errorf("%d fetches, want a retry", got)
	}

	h.m.open(opened{kind: itemPlaylist, id: "nope"})
	h.send(postedMsg{})
	h.loaded()
	h.screenHas("Nothing here", "This page may have moved or been removed.")
}

func TestHelp(t *testing.T) {
	h := placeholderHarness(t)
	h.press("?")
	h.golden("help")
	// Playback keys still work; browsing keys wait.
	h.press("space", "down")
	if got := h.ctl.take(); !reflect.DeepEqual(got, []string{"Toggle"}) {
		t.Errorf("sent %q", got)
	}
	h.press("esc")
	if h.m.isHelp {
		t.Error("esc left the help open")
	}
	h.resize(100, 14)
	h.press("?")
	h.golden("help-columns")
}

func TestHealth(t *testing.T) {
	older := remote.Peer{Hello: api.Hello{Version: "0.3.0"}, Version: "0.4.0", IsCompatible: true}
	cases := []struct {
		name string
		link remote.Link
		text string
		r    string // what R asks of the Supervisor
	}{
		{"connecting", remote.Link{Health: remote.Connecting}, "Connecting to the background player…", "Retry"},
		{"reconnecting", remote.Link{Health: remote.Reconnecting, Connection: 1}, "The background player stopped.", "Retry"},
		{"unresponsive", remote.Link{Health: remote.Unresponsive, Connection: 1}, "isn’t responding.  R Restart", "Restart"},
		{"unavailable", remote.Link{Health: remote.Unavailable, Connection: 1}, "reach the background player.  R Retry  Log: /logs/ytmd.log", "Retry"},
		{"daemon-outdated", remote.Link{Health: remote.DaemonOutdated, Daemon: older, Connection: 1}, "(0.3.0) is older than ytm (0.4.0)", "Retry"},
		{"frontend-outdated", remote.Link{Health: remote.FrontendOutdated, Daemon: older}, "is newer than ytm", "Retry"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := placeholderHarness(t)
			h.m.setLink(tc.link)
			h.send(postedMsg{})
			h.screenHas(tc.text)
			if tc.name == "unavailable" {
				h.golden("unavailable")
			}
			h.press("R")
			if !reflect.DeepEqual(h.sup.calls, []string{tc.r}) {
				t.Errorf("R asked %q, want %s", h.sup.calls, tc.r)
			}
		})
	}
	h := placeholderHarness(t)
	if h.press("R") != nil || len(h.sup.calls) > 0 {
		t.Error("R did something while connected")
	}
}

// A new connection fetches again what failed meanwhile.
func TestReconnectRetries(t *testing.T) {
	h := newHarness(t, catalog.NewPlaceholderSource(0))
	h.m.session.ApplyFailed(api.FailedEvent{Part: api.PartLiked, Err: &api.Error{Code: api.CodeInternal}})
	h.m.setLink(remote.Link{Health: remote.Connected, Connection: 2})
	if !slices.Contains(h.daemon.calls, "Refresh 3") {
		t.Errorf("asked %q, not to refresh the likes", h.daemon.calls)
	}
}

func TestNotices(t *testing.T) {
	h := placeholderHarness(t)
	h.m.tell(notice.Notice{Kind: notice.Failure, Title: notice.ControlFailed, Detail: notice.Reason, Args: []string{"no song"}})
	cmd := h.send(postedMsg{})
	h.screenHas("Couldn’t change playback: no song")
	seq := h.m.noticeSeq
	if cmd == nil {
		t.Fatal("no timer ends the notice")
	}
	h.send(clearNoticeMsg{seq: seq - 1})
	h.screenHas("Couldn’t change playback")
	h.send(clearNoticeMsg{seq: seq})
	if strings.Contains(h.screen(), "Couldn’t change playback") {
		t.Error("the notice outlived its time")
	}
}

func TestTicks(t *testing.T) {
	h := placeholderHarness(t)
	if h.m.isTicking {
		t.Fatal("paused, yet ticking")
	}
	h.mirror.state.Playing = true
	h.mirror.state.Position = 41.25
	if cmd := h.send(postedMsg{}); cmd == nil || !h.m.isTicking {
		t.Fatal("playing, yet not ticking")
	}
	if d := h.m.untilNextSecond(); d < 750_000_000 || d > 800_000_000 {
		t.Errorf("the next tick in %v, want at the next second", d)
	}
	h.send(tea.BlurMsg{})
	h.send(tickMsg{})
	if h.m.isTicking {
		t.Error("blurred, yet ticking")
	}
	h.send(tea.FocusMsg{})
	if !h.m.isTicking {
		t.Error("focused again, yet not ticking")
	}
	if !reflect.DeepEqual(h.sup.calls, []string{"Foreground"}) {
		t.Errorf("focus asked %q of the Supervisor", h.sup.calls)
	}
	h.mirror.state.Playing = false
	h.send(tickMsg{})
	if h.m.isTicking {
		t.Error("paused, yet ticking")
	}
}

func TestPaste(t *testing.T) {
	h := placeholderHarness(t)
	h.send(tea.PasteMsg{Content: "paper\nmoons"})
	if h.m.query != "" {
		t.Error("a paste outside the input typed")
	}
	h.press("/")
	h.send(tea.PasteMsg{Content: "paper\nmoons"})
	if h.m.query != "paper moons" {
		t.Errorf("pasted %q", h.m.query)
	}
}

func TestTraditionalChinese(t *testing.T) {
	inLanguage(t, "zh-Hant")
	h := placeholderHarness(t)
	h.mirror.state.Repeat = api.RepeatOne
	h.send(postedMsg{})
	h.golden("queue-zh-Hant")
	h.press("?")
	h.golden("help-zh-Hant")
	h.press("?", "shift+tab")
	h.m.session.ApplyAccount(api.AccountEvent{SignedIn: false})
	h.send(postedMsg{})
	h.screenHas("尚未登入", "o 開啟 Desktop")
}

// Titles are cut by their width on screen: wide characters take two
// cells.
func TestWideTitles(t *testing.T) {
	h := placeholderHarness(t)
	songs := []catalog.Song{
		{ID: "wide", Title: "夜に駆ける夜に駆ける夜に駆ける夜に駆ける", Artist: "YOASOBI", Album: "THE BOOK", Seconds: 261},
		{ID: "emoji", Title: "🎧🎧🎧 Headphones on, the very long title of a song", Artist: "Someone", Seconds: 200},
	}
	h.mirror.setQueue(songs)
	h.send(postedMsg{})
	h.resize(60, 12)
	h.golden("queue-wide-titles")
	for line := range strings.Lines(h.screen()) {
		if w := width(strings.TrimRight(line, "\n")); w > 60 {
			t.Errorf("a line %d cells wide: %q", w, line)
		}
	}
}

func TestTinyTerminal(t *testing.T) {
	h := placeholderHarness(t)
	for _, size := range [][2]int{{80, 6}, {80, 3}, {10, 24}, {1, 1}} {
		h.resize(size[0], size[1])
		lines := strings.Count(h.m.content, "\n") + 1
		if lines > size[1] {
			t.Errorf("%v: %d lines", size, lines)
		}
	}
	h.resize(80, 6)
	h.golden("tiny")
}

func longQueue(n int) []catalog.Song {
	songs := make([]catalog.Song, n)
	for i := range songs {
		id := "song" + strconv.Itoa(i)
		songs[i] = catalog.Song{ID: id, Title: "Song " + strconv.Itoa(i), Artist: "Artist " + strconv.Itoa(i%7), Album: "Album " + strconv.Itoa(i%13), Seconds: 120 + i%180}
	}
	return songs
}
