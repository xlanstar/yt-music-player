package tui

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/remote"
	"yt-music-player/internal/termimage"
)

// The queue's cursor follows the current song while the queue does not
// show too, until moved.
func TestPrepareFollowsQueue(t *testing.T) {
	h := placeholderHarness(t)
	songs := longQueue(300)
	h.mirror.setQueue(songs)
	playing := func(i int) {
		h.mirror.state.Index, h.mirror.state.Current = i, songs[i].ID
		h.send(postedMsg{})
	}
	playing(150)
	if !strings.Contains(h.selectedLine(), "Song 150 ") {
		t.Errorf("selected %q, want the current song", h.selectedLine())
	}

	h.press("shift+tab")
	playing(200)
	h.press("tab")
	if !strings.Contains(h.selectedLine(), "Song 200 ") {
		t.Errorf("back on the queue, selected %q, want the current song", h.selectedLine())
	}
	h.press("?")
	playing(210)
	h.press("?")
	if !strings.Contains(h.selectedLine(), "Song 210 ") {
		t.Errorf("after the help, selected %q, want the current song", h.selectedLine())
	}

	h.press("down")
	playing(5)
	if !strings.Contains(h.selectedLine(), "Song 211 ") {
		t.Errorf("moved, selected %q, want where it moved", h.selectedLine())
	}
}

// A resize scrolls the list to keep its cursor shown in the rows left.
func TestPrepareFitsResize(t *testing.T) {
	h := placeholderHarness(t)
	songs := longQueue(300)
	h.mirror.setQueue(songs)
	h.mirror.state.Index, h.mirror.state.Current = 150, songs[150].ID
	h.send(postedMsg{})
	h.press("down", "down")
	for _, size := range [][2]int{{80, 10}, {80, 7}, {80, 6}, {80, 40}, {40, 12}} {
		h.resize(size[0], size[1])
		if size[1] > 6 && !strings.Contains(h.selectedLine(), "Song 152 ") {
			t.Errorf("at %v, selected %q, want Song 152", size, h.selectedLine())
		}
		f := h.m.prepare()
		if rows := f.content.Dy(); rows > 0 && (h.m.queue.offset > 152 || 152 >= h.m.queue.offset+rows) {
			t.Errorf("at %v, offset %d hides the cursor in %d rows", size, h.m.queue.offset, rows)
		}
	}
}

// flaky is the placeholder's source, whose albums fail while fails is set.
type flaky struct {
	*catalog.PlaceholderSource
	fails atomic.Bool
	calls atomic.Int32
}

func (f *flaky) Album(ctx context.Context, id string) (catalog.Album, []catalog.Song, error) {
	f.calls.Add(1)
	if f.fails.Load() {
		return catalog.Album{}, nil, errors.New("the network is down")
	}
	return f.PlaceholderSource.Album(ctx, id)
}

// Keys act on the page as it is, not on a frame prepared before it
// changed: no retry once loaded, no Play all once another tab shows.
func TestPrepareActsAfresh(t *testing.T) {
	src := &flaky{PlaceholderSource: catalog.NewPlaceholderSource(0)}
	src.fails.Store(true)
	h := newHarness(t, src)
	album := catalog.Placeholder().Albums[0]
	h.press("shift+tab")
	h.m.open(opened{kind: itemAlbum, id: album.ID})
	h.send(postedMsg{})
	h.loaded()
	if f := h.m.prepare(); f.page.retry == nil {
		t.Fatalf("the failed album's frame has no retry:\n%s", h.screen())
	}

	// A new connection loads it again, behind the frame's back.
	src.fails.Store(false)
	h.m.setLink(remote.Link{Health: remote.Connected, Connection: 2})
	h.loaded()
	calls := src.calls.Load()
	h.press("enter")
	h.loaded()
	if got := src.calls.Load(); got != calls {
		t.Errorf("enter fetched %d times more, want none once loaded", got-calls)
	}
	if got := h.ctl.take(); len(got) != 1 || !strings.HasPrefix(got[0], "PlayAll ") {
		t.Errorf("enter sent %q, want Play all", got)
	}

	h.m.prepare()
	h.press("tab", "enter")
	if got := h.ctl.take(); len(got) != 1 || !strings.HasPrefix(got[0], "Play ") {
		t.Errorf("enter on the queue sent %q, want its song", got)
	}
}

// A list shows as many rows as prepare fitted it to, its cursor among
// them, at every height, with and without the banner.
func TestPrepareFitRowsDrawn(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(h *harness)
	}{
		{"queue-5000", viewCases[0].prepare},
		{"search", viewCases[2].prepare},
		{"album", func(h *harness) {
			h.press("down")
			h.m.open(opened{kind: itemAlbum, id: catalog.Placeholder().Albums[0].ID})
			h.send(postedMsg{})
			h.loaded()
			h.press("end")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := viewing(t, tc.prepare)
			for _, health := range []remote.Health{remote.Connected, remote.Unresponsive} {
				h.m.setLink(remote.Link{Health: health, Connection: h.m.link.Connection})
				for ht := 1; ht <= 30; ht++ {
					h.resize(80, ht)
					checkListDrawn(t, h)
				}
			}
		})
	}
}

// checkListDrawn draws a prepared frame and checks its list's rows.
func checkListDrawn(t *testing.T, h *harness) {
	t.Helper()
	f := h.m.prepare()
	h.m.draw(&f)
	p := &f.page
	if p.list == nil || p.title != "" {
		return
	}
	l, rows := p.list, f.content.Dy()
	lines := strings.Split(h.screen(), "\n")
	drawn := 0
	for y := f.content.Min.Y; y < f.content.Max.Y; y++ {
		if lines[y] != "" {
			drawn++
		}
	}
	want := min(rows, p.n-l.offset)
	if drawn != want {
		t.Errorf("%d lines: %d rows drawn, want %d of %d rows", h.m.height, drawn, want, rows)
	}
	if rows > 0 && (l.cursor < l.offset || l.cursor >= l.offset+rows) {
		t.Errorf("%d lines: cursor %d hidden from offset %d in %d rows", h.m.height, l.cursor, l.offset, rows)
	}
}

// drawState is what a draw must leave as it found it.
type drawState struct {
	lists      [3]listState
	collection [numTabs]listState
	follows    bool
	memo       memo
	frame      uint64
	covers     [numCovers]coverState
	loading    bool
	published  uint64
}

type coverState struct {
	key            coverKey
	frame          uint64
	srcPx          int
	ready, running bool
}

func (h *harness) drawState() drawState {
	g := &h.m.covers
	s := drawState{
		lists:      [3]listState{h.m.queue, h.m.search, h.m.library},
		collection: h.m.collection,
		follows:    h.m.queueFollows,
		memo:       h.m.memo,
		frame:      g.frame,
		loading:    g.art != nil && g.art.Loading(),
		published:  g.published,
	}
	for i := range g.cache {
		c := &g.cache[i]
		s.covers[i] = coverState{c.key, c.frame, c.srcPx, c.isReady, c.job.IsRunning()}
	}
	return s
}

// drawn is what a draw makes: the scene, composed without its covers'
// lines, its hits and its covers.
type drawn struct {
	content string
	hits    []hit
	covers  []coverRequest
}

func (h *harness) drawn() drawn {
	sc := &h.m.scene
	return drawn{sc.compose(&coverFrame{}), slices.Clone(sc.hits), slices.Clone(sc.covers)}
}

// Drawing a prepared frame changes nothing it read, starts nothing, and
// draws the same again.
func TestDrawTwice(t *testing.T) {
	art := albumArt{catalog.NewPlaceholderSource(0)}
	for _, tc := range []struct {
		name    string
		prepare func(h *harness)
		// src is the album's source, if not the placeholder's.
		src catalog.Source
	}{
		{"queue-5000", viewCases[0].prepare, nil},
		{"library", viewCases[1].prepare, nil},
		{"search", viewCases[2].prepare, nil},
		{"help", viewCases[3].prepare, nil},
		{"queue-cover-sixel", viewCases[5].prepare, nil},
		{"album-covers-loading", func(h *harness) {
			host := newCoverHost(h.t)
			host.hold = make(chan struct{})
			h.withCovers(termimage.Blocks, host)
			h.t.Cleanup(func() { close(host.hold) })
			h.m.open(opened{kind: itemAlbum, id: catalog.Placeholder().Albums[0].ID})
			h.send(postedMsg{})
			h.loaded()
		}, art},
		{"album-covers-sixel", func(h *harness) {
			h.withCovers(termimage.Sixel, newCoverHost(h.t))
			h.m.open(opened{kind: itemAlbum, id: catalog.Placeholder().Albums[0].ID})
			h.covered()
		}, art},
		{"short", func(h *harness) { h.resize(80, 3) }, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var h *harness
			if tc.src == nil {
				h = viewing(t, tc.prepare)
			} else {
				h = newHarness(t, tc.src)
				tc.prepare(h)
			}
			f := h.m.prepare()
			before := h.drawState()
			h.m.draw(&f)
			first := h.drawn()
			if got := h.drawState(); !reflect.DeepEqual(got, before) {
				t.Errorf("a draw changed\n%+v\nto\n%+v", before, got)
			}
			h.m.draw(&f)
			if got := h.drawState(); !reflect.DeepEqual(got, before) {
				t.Errorf("a second draw changed\n%+v\nto\n%+v", before, got)
			}
			if got := h.drawn(); !reflect.DeepEqual(got, first) {
				t.Errorf("a second draw drew\n%+v\nnot\n%+v", got, first)
			}
		})
	}
}

func TestSplitBody(t *testing.T) {
	body := band(80, 2, 5)
	for _, tc := range []struct {
		headerRows      int
		header, content [2]int
	}{
		{0, [2]int{2, 2}, [2]int{2, 7}},
		{3, [2]int{2, 5}, [2]int{5, 7}},
		{5, [2]int{2, 7}, [2]int{7, 7}},
		// A header taller than the body leaves no rows.
		{9, [2]int{2, 7}, [2]int{7, 7}},
	} {
		header, content := splitBody(body, tc.headerRows)
		if header != band(80, tc.header[0], tc.header[1]-tc.header[0]) ||
			content != band(80, tc.content[0], tc.content[1]-tc.content[0]) {
			t.Errorf("%d header rows: header %v, content %v", tc.headerRows, header, content)
		}
	}
}
