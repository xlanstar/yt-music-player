package webm

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"
	"yt-music-player/internal/budget"
)

func TestBudgetOpen(t *testing.T) {
	for _, f := range benchFiles(t) {
		t.Run(f.name, func(t *testing.T) {
			budget.Allocs(t, f.openLimit, 50, func() { _, sinkErr = Open(bytes.NewReader(f.data)) })
		})
	}
}

func TestBudgetReadFrame(t *testing.T) {
	for _, f := range benchFiles(t) {
		t.Run(f.name, func(t *testing.T) {
			r, err := Open(bytes.NewReader(f.data))
			if err != nil {
				t.Fatal(err)
			}
			readThrough(t, r)
			// 0 allocs a frame today: a block reuses the last one's buffer.
			// Per frame, not per file, which takes long enough for another
			// goroutine's allocation to count.
			budget.Allocs(t, 0, 1000, func() {
				f, err := r.ReadFrame()
				if errors.Is(err, io.EOF) {
					_, err = r.Seek(0)
				}
				if err != nil {
					t.Fatal(err)
				}
				sinkFrame = f
			})
		})
	}
}

func TestBudgetSeek(t *testing.T) {
	r, err := Open(bytes.NewReader(benchFiles(t)[1].data))
	if err != nil {
		t.Fatal(err)
	}
	// The first seek builds the runtime's caches.
	sinkTime, sinkErr = r.Seek(0)
	// 0 allocs today.
	budget.Allocs(t, 0, 100, func() { sinkTime, sinkErr = r.Seek(4 * time.Second) })
}
