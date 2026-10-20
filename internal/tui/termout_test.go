package tui

import (
	"bytes"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"yt-music-player/internal/termimage"
)

// pipeOut is a termOut over a pipe and what went through it, once closed.
func pipeOut(t *testing.T) (o *termOut, written func() string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	got := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(r)
		got <- b
	}()
	o = newTermOut(w)
	o.resize(80, 24)
	return o, func() string {
		_ = w.Close()
		return string(<-got)
	}
}

// barTable is generation gen of a table with the bar's cover on line y.
func barTable(gen uint64, y int) *termimage.Table {
	return termimage.NewTable(gen, []termimage.Placement{
		{Seq: "IMG" + strconv.Itoa(y), X: 1, Y: y, Cols: 4, Rows: 2},
	})
}

// marked is a synchronized frame that writes the bar's cover under gen.
func marked(gen uint64) []byte {
	return []byte("\x1b[?2026h " + termimage.Mark(gen, int(slotBar), 0) + "⠀⠀⠀⠀\x1b[?2026l")
}

// TestTermOutTables draws a frame's overlays from the table of its
// generation, the last published or the one before, and none from older.
func TestTermOutTables(t *testing.T) {
	o, written := pipeOut(t)
	write := func(frame []byte) {
		t.Helper()
		if n, err := o.Write(frame); n != len(frame) || err != nil {
			t.Fatalf("Write = %d, %v; want %d", n, err, len(frame))
		}
	}
	write([]byte("\x1b[c"))
	o.publish(barTable(1, 21))
	write(marked(1))
	o.publish(barTable(2, 20))
	// Written while the next published: its own table's place.
	write(marked(1))
	write(marked(2))
	o.publish(barTable(3, 19))
	write(marked(1))
	o.resize(80, 21)
	write(marked(3))
	const frame = "\x1b[?2026h ⠀⠀⠀⠀\x1b[?2026l"
	want := "\x1b[c" +
		"\x1b[?2026h ⠀⠀⠀⠀\x1b7\x1b[22;2HIMG21\x1b8\x1b[?2026l" +
		"\x1b[?2026h ⠀⠀⠀⠀\x1b7\x1b[22;2HIMG21\x1b8\x1b[?2026l" +
		"\x1b[?2026h ⠀⠀⠀⠀\x1b7\x1b[21;2HIMG20\x1b8\x1b[?2026l" +
		frame + // generation 1 is gone
		frame // line 19 is too low for 21 lines
	if got := written(); got != want {
		t.Errorf("wrote\n%q\nwant\n%q", got, want)
	}
	if n := o.missing.Load(); n != 1 {
		t.Errorf("missing = %d, want generation 1's last frame", n)
	}
}

// TestTermOutFails reports nothing written when the terminal takes
// nothing.
func TestTermOutFails(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	o := newTermOut(w)
	t.Cleanup(func() { _ = o.Close() })
	o.publish(barTable(1, 21))
	for _, frame := range [][]byte{[]byte("plain"), marked(1)} {
		if n, err := o.Write(frame); n != 0 || err == nil {
			t.Errorf("Write(%q) = %d, %v; want 0 and an error", frame, n, err)
		}
	}
}

// TestTermOutConcurrent writes from two goroutines, as Bubble Tea's
// renderer and event loop may, while the UI publishes: every frame goes
// whole, with its overlay.
func TestTermOutConcurrent(t *testing.T) {
	o, written := pipeOut(t)
	o.publish(barTable(1, 21))
	const frames = 200
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			for range frames {
				if _, err := o.Write(marked(1)); err != nil {
					t.Error(err)
					return
				}
				if _, err := o.Write([]byte("\x1b[?2026h" + strings.Repeat("x", 100) + "\x1b[?2026l")); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Go(func() {
		for range frames {
			o.publish(barTable(1, 21))
			o.resize(80, 24)
		}
	})
	wg.Wait()
	got := written()
	for f := range strings.SplitSeq(strings.TrimSuffix(got, "\x1b[?2026l"), "\x1b[?2026l") {
		if f != "\x1b[?2026h ⠀⠀⠀⠀\x1b7\x1b[22;2HIMG21\x1b8" && f != "\x1b[?2026h"+strings.Repeat("x", 100) {
			t.Fatalf("a frame went as %q", f)
		}
	}
	if n := bytes.Count([]byte(got), []byte("IMG21")); n != 2*frames {
		t.Errorf("%d overlays, want %d", n, 2*frames)
	}
}
