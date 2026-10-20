package tui

import (
	"context"
	"fmt"
	"image"
	"slices"
	"strings"
	"testing"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/remote"
	"yt-music-player/internal/termimage"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The screens' layout as it is, to hold while render changes: each golden
// file shows a screen at several sizes.

// wideSongs are songs of wide characters, the first long enough to cut.
var wideSongs = []catalog.Song{
	{ID: "yoru", Title: "夜に駆ける・群青・アイドル・怪物・祝福", Artist: "YOASOBI", Album: "THE BOOK", Seconds: 261},
	{ID: "lemon", Title: "Lemon", Artist: "米津玄師", Album: "STRAY SHEEP", Seconds: 255},
	{ID: "kaze", Title: "風になる", Artist: "つじあやの", Album: "猫の恩返し サウンドトラック", Seconds: 300},
	{ID: "luck", Title: "小幸運", Artist: "田馥甄", Album: "我的少女時代 電影原聲帶", Seconds: 265},
}

// The selected row and the current song's are apart.
func TestQueueSelected(t *testing.T) {
	h := placeholderHarness(t)
	h.press("down", "down")
	h.golden("queue-selected")
}

func TestSearchTyping(t *testing.T) {
	h := placeholderHarness(t)
	h.press("/")
	h.golden("search-typing")
	h.typeText("moon")
	h.loaded()
	// Typed on, the last results show until the new ones come.
	h.typeText(" x")
	h.golden("search-refining")
}

// TestSearchCursor shows the terminal's cursor after the query while
// typing, which an input method's window follows; screen draws it as █.
func TestSearchCursor(t *testing.T) {
	h := placeholderHarness(t)
	cursor := func() *tea.Cursor { return h.m.View().Cursor }
	if c := cursor(); c != nil {
		t.Fatalf("browsing, the cursor shows at %v", c.Position)
	}
	h.press("/")
	h.typeText("月の")
	if c := cursor(); c == nil || c.Position != (tea.Position{X: 7, Y: tabsRows}) {
		t.Fatalf("typing, the cursor is %v; want after the query, (7, %d)", c, tabsRows)
	}
	// Cut, the query ends in an ellipsis, which the cursor sits on.
	h.typeText(strings.Repeat("x", 80))
	if !strings.Contains(h.m.content, "x…") {
		t.Errorf("the query is not cut:\n%s", h.screen())
	}
	if c := cursor(); c == nil || c.X != h.m.width-1 {
		t.Errorf("past the edge, the cursor is %v; want on the last cell", c)
	}
	// No input shows, no cursor: the body has no rows.
	h.resize(80, barRows)
	if c := cursor(); c != nil {
		t.Errorf("without the input, the cursor shows at %v", c.Position)
	}
	h.resize(80, 24)
	h.press("esc")
	if c := cursor(); c != nil {
		t.Errorf("done typing, the cursor shows at %v", c.Position)
	}
}

// Every size of the queue: the columns a row shows, the app's name, the
// bar's cuts.
func TestWidths(t *testing.T) {
	h := placeholderHarness(t)
	h.press("down")
	goldenText(t, "widths", h.screens(
		[2]int{20, 8}, [2]int{37, 8}, [2]int{38, 8}, [2]int{59, 8}, [2]int{60, 8},
		[2]int{63, 8}, [2]int{64, 8}, [2]int{120, 8}, [2]int{200, 8},
	))
}

// A short terminal shows the body down to one line, then the bar alone,
// then the bar's last lines; a banner goes before the bar's top.
func TestShortTerminals(t *testing.T) {
	h := placeholderHarness(t)
	got := h.screens([2]int{80, 7}, [2]int{80, 6}, [2]int{80, 5}, [2]int{80, 4},
		[2]int{80, 3}, [2]int{80, 2}, [2]int{80, 1})
	h.m.setLink(remote.Link{Health: remote.Unresponsive, Connection: 1})
	got += "--- unresponsive\n" + h.screens([2]int{80, 8}, [2]int{80, 7}, [2]int{80, 5},
		[2]int{80, 4}, [2]int{80, 3})
	goldenText(t, "short", got)
}

// TestBanners shows the daemon's health, with and without a key that
// helps, over a short page.
func TestBanners(t *testing.T) {
	older := remote.Peer{Hello: api.Hello{Version: "0.3.0"}, Version: "0.4.0", IsCompatible: true}
	incompatible := older
	incompatible.IsCompatible = false
	var got strings.Builder
	for _, tc := range []struct {
		name string
		link remote.Link
	}{
		{"connecting", remote.Link{Health: remote.Connecting}},
		{"reconnecting", remote.Link{Health: remote.Reconnecting, Connection: 1}},
		{"unresponsive", remote.Link{Health: remote.Unresponsive, Connection: 1}},
		{"unavailable", remote.Link{Health: remote.Unavailable, Connection: 1}},
		{"daemon-outdated", remote.Link{Health: remote.DaemonOutdated, Daemon: older, Connection: 1}},
		{"daemon-incompatible", remote.Link{Health: remote.DaemonOutdated, Daemon: incompatible, Connection: 1}},
		{"frontend-outdated", remote.Link{Health: remote.FrontendOutdated, Daemon: older}},
	} {
		h := placeholderHarness(t)
		h.m.setLink(tc.link)
		h.send(postedMsg{})
		fmt.Fprintf(&got, "--- %s\n", tc.name)
		got.WriteString(h.screens([2]int{80, 9}, [2]int{40, 9}))
	}
	goldenText(t, "banners", got.String())
}

func TestHelpNarrow(t *testing.T) {
	h := placeholderHarness(t)
	h.press("?")
	goldenText(t, "help-narrow", h.screens([2]int{40, 24}, [2]int{60, 10}, [2]int{30, 8}))
}

// Wide characters cut whole, in the bar and the rows, at odd widths too.
func TestWideScreens(t *testing.T) {
	h := placeholderHarness(t)
	h.mirror.setQueue(wideSongs)
	h.send(postedMsg{})
	h.press("down")
	goldenText(t, "wide", h.screens([2]int{41, 10}, [2]int{50, 10}, [2]int{61, 10}, [2]int{81, 10}))

	h.withCovers(termimage.Blocks, newCoverHost(t))
	h.covered()
	goldenText(t, "wide-cover-blocks", h.screens([2]int{45, 10}, [2]int{61, 10}))
}

// wideAlbum is the placeholder whose albums have wide names and covers
// fetched from coverURL.
type wideAlbum struct{ *catalog.PlaceholderSource }

func (s wideAlbum) Album(ctx context.Context, id string) (catalog.Album, []catalog.Song, error) {
	a, songs, err := s.PlaceholderSource.Album(ctx, id)
	a.Title, a.Artist, a.Art = "猫の恩返し サウンドトラック 完全版", "野見祐二", coverURL
	return a, songs, err
}

// A collection's header: its title and subtitle, beside the cover.
func TestCollectionHeader(t *testing.T) {
	h := newHarness(t, albumArt{catalog.NewPlaceholderSource(0)})
	h.withCovers(termimage.Blocks, newCoverHost(t))
	h.m.open(opened{kind: itemAlbum, id: catalog.Placeholder().Albums[0].ID})
	h.covered()
	h.golden("album-cover-blocks")
	// Too narrow for covers, then too short for the header's.
	goldenText(t, "album-cover-blocks-sizes", h.screens([2]int{44, 12}, [2]int{43, 12}, [2]int{80, 8}, [2]int{80, 7}))

	h = newHarness(t, wideAlbum{catalog.NewPlaceholderSource(0)})
	h.withCovers(termimage.Blocks, newCoverHost(t))
	h.m.open(opened{kind: itemAlbum, id: catalog.Placeholder().Albums[0].ID})
	h.covered()
	goldenText(t, "album-wide", h.screens([2]int{51, 12}, [2]int{80, 12}))
}

// Every screen stays inside the terminal at every size up to 90×26, by
// either width method: its lines, hits, covers, overlays and cursor.
// Drawing over cells drawn panics (panicOnOverlap).
func TestScreensFit(t *testing.T) {
	album := func(p termimage.Protocol) func(h *harness) {
		return func(h *harness) {
			h.withCovers(p, newCoverHost(h.t))
			h.m.open(opened{kind: itemAlbum, id: catalog.Placeholder().Albums[0].ID})
		}
	}
	for _, tc := range []struct {
		name    string
		prepare func(h *harness)
	}{
		{"wide-queue", func(h *harness) {
			h.withCovers(termimage.Sixel, newCoverHost(h.t))
			h.mirror.setQueue(append(slices.Clone(wideSongs), emojiSongs...))
			h.mirror.state.Playing = true
			h.send(postedMsg{})
			h.press("down", "l")
		}},
		{"help", func(h *harness) { h.press("?") }},
		{"typing", func(h *harness) {
			h.press("/")
			h.typeText("夜明けのアーケード")
		}},
		{"notice", func(h *harness) {
			h.m.say("夜明けのアーケード, a notice longer than most")
			// Rendered, not run: the notice's end would wait for it.
			h.send(postedMsg{})
		}},
		{"banner", func(h *harness) { h.m.setLink(remote.Link{Health: remote.Unavailable, Connection: 1}) }},
		{"album-sixel", album(termimage.Sixel)},
		{"album-kitty", album(termimage.Kitty)},
		{"album-blocks", album(termimage.Blocks)},
	} {
		for _, method := range []string{"wcwidth", "graphemes"} {
			t.Run(tc.name+"-"+method, func(t *testing.T) {
				h := newHarness(t, wideAlbum{catalog.NewPlaceholderSource(0)})
				if method == "graphemes" {
					h.send(unicodeCore)
				}
				tc.prepare(h)
				h.covered()
				for w := range 91 {
					for ht := range 27 {
						h.resize(w, ht)
						h.covered()
						checkFits(t, h)
					}
				}
			})
		}
	}
}

// checkFits fails unless what h's last render drew is inside its screen,
// the overlays above its last line.
func checkFits(t *testing.T, h *harness) {
	t.Helper()
	w, ht := h.m.width, h.m.height
	screen := image.Rect(0, 0, w, ht)
	if h.m.content != "" {
		lines := strings.Split(h.m.content, "\n")
		if len(lines) > ht {
			t.Fatalf("%d×%d: %d lines", w, ht, len(lines))
		}
		for y, line := range lines {
			if lw := h.m.cells.width(line); lw > w {
				t.Fatalf("%d×%d: line %d is %d cells: %q", w, ht, y, lw, ansi.Strip(line))
			}
		}
	}
	s := &h.m.scene
	for _, x := range s.hits {
		if x.shown.Empty() || !x.shown.In(screen) {
			t.Fatalf("%d×%d: a hit shows at %v", w, ht, x.shown)
		}
	}
	for _, r := range s.covers {
		if !r.rect.In(screen) {
			t.Fatalf("%d×%d: a cover at %v", w, ht, r.rect)
		}
	}
	if s.hasCursor && !s.cursor.In(screen) {
		t.Fatalf("%d×%d: the cursor at %v", w, ht, s.cursor)
	}
	for _, p := range h.m.covers.places {
		if r := image.Rect(p.X, p.Y, p.X+p.Cols, p.Y+p.Rows); p.Seq != "" && !r.In(image.Rect(0, 0, w, ht-1)) {
			t.Fatalf("%d×%d: an overlay at %v", w, ht, r)
		}
	}
}
