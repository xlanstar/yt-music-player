package tui

import (
	"os"
	"sync"
	"sync/atomic"
	"yt-music-player/internal/termimage"

	"github.com/charmbracelet/x/term"
)

// termOut is the terminal Bubble Tea writes to: it draws Sixel and iTerm2
// covers after each frame that wrote their marks (termimage.Splice), so
// after any frame that wiped them, and only then.
//
// Bubble Tea writes a frame in one Write, from its renderer's goroutine,
// while its event loop may write too (restoring the terminal), hence mu.
type termOut struct {
	f  *os.File
	mu sync.Mutex
	// buf is the last frame spliced, its array the next's.
	buf []byte

	// tables are the last two generations published, at gen%2: a frame
	// written is of the last one or the one before, as Bubble Tea's renderer
	// holds its lock from taking a view until written, and the event loop
	// waits on it to hand over the next view, after one Update.
	tables [2]atomic.Pointer[termimage.Table]
	// size is the screen's, width<<32 | height: one frame never reads one
	// size's width and another's height.
	size atomic.Uint64
	// missing counts frames whose marks no table kept: it stays 0 while
	// the renderer's locks do as tables says.
	missing atomic.Uint64
}

// Bubble Tea and colorprofile ask the output for a term.File, the TTY,
// its size and colors.
var _ term.File = (*termOut)(nil)

func newTermOut(f *os.File) *termOut { return &termOut{f: f} }

// Read implements term.File.
func (o *termOut) Read(p []byte) (int, error) { return o.f.Read(p) }

// Close implements term.File.
func (o *termOut) Close() error { return o.f.Close() }

// Fd implements term.File.
func (o *termOut) Fd() uintptr { return o.f.Fd() }

// Write writes frame, its marks swapped for the overlays they ask for.
// On failure it reports nothing written, as frame and what went differ.
func (o *termOut) Write(frame []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := frame
	if gen, ok := termimage.FrameGen(frame); ok {
		t := o.tables[gen%2].Load()
		if t == nil || t.Gen() != gen {
			o.missing.Add(1)
			t = nil
		}
		size := o.size.Load()
		o.buf = termimage.Splice(o.buf[:0], frame, t, int(size>>32), int(size&0xffffffff))
		out = o.buf
	}
	if _, err := o.f.Write(out); err != nil {
		return 0, err
	}
	return len(frame), nil
}

// publish makes t the table of the frames that carry its generation, on
// the UI thread, once an Update at most: the generation after the last.
func (o *termOut) publish(t *termimage.Table) { o.tables[t.Gen()%2].Store(t) }

// resize keeps the screen's size, before the renderer takes it.
func (o *termOut) resize(w, h int) {
	o.size.Store(uint64(max(w, 0))<<32 | uint64(max(h, 0)))
}
