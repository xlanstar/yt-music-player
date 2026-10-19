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

func TestBudgetUnlace(t *testing.T) {
	for _, tc := range laceCases {
		t.Run(tc.name, func(t *testing.T) {
			data := lacedBlock(tc.kind, tc.frames)
			r := &Reader{}
			// The first block makes the frames' slice; the next reuse it.
			frames, err := r.unlace(tc.kind, data)
			if err != nil {
				t.Fatal(err)
			}
			if len(frames) != tc.frames {
				t.Fatalf("%d frames, want %d", len(frames), tc.frames)
			}
			for i, f := range frames {
				if len(f) == 0 || f[0] != byte(i) || f[len(f)-1] != byte(i) {
					t.Fatalf("frame %d is not its bytes: sizes misread", i)
				}
			}
			r.laced = frames
			budget.Allocs(t, tc.limit, 100, func() { r.laced, sinkErr = r.unlace(tc.kind, data) })
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
