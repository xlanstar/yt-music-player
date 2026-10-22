package main

import (
	"context"
	"sync"
	"testing"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/audio"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/stream"

	"github.com/egoist/mygo/ui"
)

// instantResolver answers at once with a stream named after the song, or
// with err.
type instantResolver struct {
	mu  sync.Mutex
	err error
}

func (r *instantResolver) Resolve(_ context.Context, id string) (stream.Stream, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return stream.Stream{}, r.err
	}
	return stream.Stream{URL: "https://audio/" + id}, nil
}

func (r *instantResolver) Forget(string) {}

func (r *instantResolver) fail(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
}

// playbackApp is a testApp whose ytmd plays, through res, on a fake
// engine.
func playbackApp(tb testing.TB, res stream.Resolver) (*app, posts, *testEngine) {
	tb.Helper()
	a, q := startApp(tb, catalog.NewPlaceholderSource(0), ytmdOptions{resolver: res})
	q.ytmd.until(tb, "fetched the account", func() bool { return !a.session.IsBusy() })
	return a, q, q.ytmd.engine
}

// startPlaying plays the current song from the start, through its load,
// until the engine reports it ready.
func startPlaying(tb testing.TB, a *app, q posts, e *testEngine) {
	tb.Helper()
	a.client.Seek(0)
	a.client.Resume()
	q.ytmd.until(tb, "loaded", func() bool { return e.loads() > 0 })
	e.do(func(f *audio.Fake) { f.Dur = 200 })
	e.emit(audio.StreamReady)
	syncDaemon(tb, q)
	e.do(func(f *audio.Fake) { f.TakeCalls() })
}

func TestPlaybackThroughTheView(t *testing.T) {
	res := &instantResolver{}
	a, q, e := playbackApp(t, res)
	tt := newTester(a)
	click(t, tt, "Play")
	eventually(t, tt, q, "played", func() bool { return e.isPlaying() && e.loads() == 1 })
	res.fail(stream.ErrUnplayable)
	click(t, tt, "Next")
	eventually(t, tt, q, "stopped", func() bool {
		return toasted(tt, "isn’t available") && tt.HasText("Playback stopped: 5 songs in a row couldn’t play.")
	})
}

// Waiting for a song (always, with nothing to play it), the position stands
// still: no frame moves it.
func TestNoFramesWhileWaiting(t *testing.T) {
	a := idleApp(t)
	a.client.Resume()
	st := state(t, a)
	if !st.Playing || !st.Waiting {
		t.Fatalf("state %+v, want playing and waiting", st)
	}
	if d := a.progress.nextFrame(&st, st.Position); d != 0 {
		t.Errorf("a step in %v while waiting", d)
	}
}

// The player bar and panel allocate no more playing than paused in frames
// between position steps, the frames a playing window draws most.
func TestPlayerChromeAllocations(t *testing.T) {
	a, q, e := playbackApp(t, &instantResolver{})
	startPlaying(t, a, q, e)
	a.client.Seek(30.5)
	syncDaemon(t, q)
	tt := ui.NewTester(func(c *ui.Context) {
		a.syncPlayback(c)
		ui.Row(c).Fill().Children(func() { a.nowPlaying(c, 0) })
		a.playerBar(c)
	}, 1360, 860)
	allocs := func() float64 {
		for range 30 {
			tt.Frame() // animations settle, caches fill
		}
		return testing.AllocsPerRun(50, tt.Frame)
	}
	playing := allocs()
	a.client.Toggle()
	syncDaemon(t, q)
	paused := allocs()
	t.Logf("allocations per frame: %v playing, %v paused", playing, paused)
	if playing > paused {
		t.Errorf("%v allocations per frame playing, %v paused", playing, paused)
	}
}

func TestEqualizerRestsAwayFromFocus(t *testing.T) {
	a, _ := testApp(t)
	a.client.Resume()
	settled(t, a)
	if !a.eqDancing() {
		t.Error("the equalizer rests while playing in the focused window")
	}
	a.progress.setFocused(false)
	if a.eqDancing() {
		t.Error("the equalizer dances in an unfocused window")
	}
	a.progress.setFocused(true)
	a.progress.setShown(false)
	if a.eqDancing() {
		t.Error("the equalizer dances in a hidden window")
	}
	a.progress.setShown(true)
	a.client.Pause()
	settled(t, a)
	if a.eqDancing() {
		t.Error("the equalizer dances while paused")
	}
}

// The position's frames come as its step turns: just after each second
// while focused, on whole multiples of 5 s while not, never hidden.
func TestProgressSteps(t *testing.T) {
	playing := api.State{Playing: true, Current: "s", Duration: 200}
	for _, tc := range []struct {
		name             string
		focused, visible bool
		s                api.State
		pos              float64
		want             time.Duration
	}{
		{"focused", true, true, playing, 30.25, 750*time.Millisecond + tickSlack},
		{"unfocused", false, true, playing, 61, 4*time.Second + tickSlack},
		{"hidden", true, false, playing, 30, 0},
		{"paused", true, true, api.State{Current: "s", Duration: 200}, 30, 0},
		{"waiting", true, true, api.State{Playing: true, Waiting: true, Current: "s"}, 30, 0},
		{"ended", true, true, playing, 200, 0},
		{"empty", true, true, api.State{Playing: true}, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var p progress
			p.setFocused(tc.focused)
			p.setShown(tc.visible)
			if got := p.nextFrame(&tc.s, tc.pos); got != tc.want {
				t.Errorf("next frame in %v, want %v", got, tc.want)
			}
		})
	}
}

// A State that comes while the seek bar is dragged leaves it under the
// pointer; let go, it shows the daemon's position again.
func TestDraggedSliderHolds(t *testing.T) {
	a := idleApp(t)
	q := ytmdOf(a).q
	tt := newTester(a)
	r := find(t, tt, "Seek")
	y := r.Y + r.H/2
	tt.Press(r.X+r.W*0.25, y)
	tt.Move(r.X+r.W*0.5, y)
	tt.Frame()
	dragged := a.bar.pos.v
	if st := state(t, a); dragged <= 0 || st.Position != dragged {
		t.Fatalf("dragged to %v, the daemon at %v", dragged, st.Position)
	}
	// Another front-end seeks.
	call(t, peer(t, q), api.Call{Method: api.MethodSeek, Seek: &api.SeekArgs{Seconds: 10}})
	if st := state(t, a); st.Position != 10 {
		t.Fatalf("the daemon at %v, want 10", st.Position)
	}
	frames(tt, 2)
	// Pulled back, the bar would jump under the pointer, which would seek
	// again.
	if st := state(t, a); a.bar.pos.v != dragged || st.Position != 10 {
		t.Fatalf("a State moved the dragged bar from %v to %v, the daemon to %v", dragged, a.bar.pos.v, st.Position)
	}
	tt.Release(r.X+r.W*0.5, y)
	frames(tt, 2)
	if st := state(t, a); a.bar.pos.v != st.Position {
		t.Errorf("let go, the bar at %v, the daemon at %v", a.bar.pos.v, st.Position)
	}
}
