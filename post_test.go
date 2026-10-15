package main

import (
	"slices"
	"sync"
	"testing"
	"yt-music-player/internal/catalog"

	"github.com/egoist/mygo/ui"
)

func TestPosterWaitsForAttach(t *testing.T) {
	var p poster
	var got []int
	for i := range 3 {
		p.post(func() { got = append(got, i) })
	}
	if len(got) != 0 {
		t.Fatalf("ran %v before attach", got)
	}
	var sent []func()
	p.attach(func(fn func()) { sent = append(sent, fn) })
	p.post(func() { got = append(got, 3) })
	for _, fn := range sent {
		fn()
	}
	if !slices.Equal(got, []int{0, 1, 2, 3}) {
		t.Fatalf("ran %v, want 0 1 2 3", got)
	}
}

// Posts racing attach are neither lost nor sent twice.
func TestPosterConcurrent(t *testing.T) {
	var p poster
	q := make(chan func(), 200)
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() { p.post(func() {}) })
	}
	p.attach(func(fn func()) { q <- fn })
	for range 100 {
		wg.Go(func() { p.post(func() {}) })
	}
	wg.Wait()
	if len(q) != 200 {
		t.Fatalf("sent %d posts, want 200", len(q))
	}
}

// The first build may run before Update is attached; its fetches still
// show once attached, with no extra frame requested.
func TestBuildBeforeAttach(t *testing.T) {
	a := newDemoApp(catalog.NewPlaceholderSource(0))
	tt := ui.NewTester(a.view, 1360, 860)
	tt.Frame()
	q := make(posts, 64)
	a.updates.attach(func(fn func()) { q <- fn })
	settleLoads(t, tt, a, q)
	wantTexts(t, tt, "Quick picks")
}

// newApp queues none of the placeholder's songs: they cannot play.
func TestNewAppWithoutDemo(t *testing.T) {
	a := newApp(catalog.NewPlaceholderSource(0))
	if len(a.cat.Songs) != 0 || a.player.Current() != "" || a.player.Position != 0 {
		t.Fatalf("started with %d songs, at %q, %v s", len(a.cat.Songs), a.player.Current(), a.player.Position)
	}
}
