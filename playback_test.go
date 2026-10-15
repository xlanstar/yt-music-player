package main

import (
	"context"
	"sync"
	"testing"
	"time"
	"yt-music-player/internal/audio"
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

func playbackApp(res stream.Resolver) (*app, posts, *audio.Fake) {
	var emit func(audio.Event)
	fake := audio.NewFake(func(ev audio.Event) { emit(ev) })
	a, q := testApp()
	a.playback.Resolver = res
	a.playback.NewEngine = func(onEvent func(audio.Event)) (audio.Engine, error) {
		emit = onEvent
		return fake, nil
	}
	return a, q, fake
}

// startPlaying plays the current song from the start, through its load,
// until the engine reports it ready.
func startPlaying(t testing.TB, a *app, q posts, fake *audio.Fake) {
	t.Helper()
	a.player.Position = 0
	a.player.Playing = true
	a.playback.Follow()
	heldPost(t, q)()
	fake.Dur = 200
	fake.Emit(audio.Ready)
	heldPost(t, q)()
	fake.TakeCalls()
}

func TestPlaybackThroughTheView(t *testing.T) {
	res := &instantResolver{}
	a, q, fake := playbackApp(res)
	tt := newTester(a)
	click(t, tt, "Play")
	settleLoads(t, tt, a, q)
	if !fake.Playing || fake.Loads != 1 {
		t.Fatalf("clicking Play: playing %v, loads %d", fake.Playing, fake.Loads)
	}
	res.fail(stream.ErrUnplayable)
	click(t, tt, "Next")
	settleLoads(t, tt, a, q)
	tt.Frame()
	if !toasted(tt, "isn’t available") {
		t.Errorf("no error announced, texts %q", tt.Texts())
	}
}

func TestNoPlaybackWithoutResolver(t *testing.T) {
	a := idleApp()
	a.playback.NewEngine = func(func(audio.Event)) (audio.Engine, error) {
		t.Error("made an engine with no resolver")
		return nil, audio.ErrUnsupported
	}
	a.player.Playing = true
	tt := newTester(a)
	tt.Frame()
	if d := a.playback.Follow(); d != 0 {
		t.Errorf("a step in %v with nothing playing", d)
	}
}

// turn moves the engine to pos and rereads it, as the frame syncPlayback
// asked for does, and returns the delay to the next frame.
func turn(a *app, fake *audio.Fake, pos float64) time.Duration {
	fake.Pos = pos
	a.playback.Resync()
	return a.playback.Follow()
}

// The player bar and panel allocate no more playing than paused in frames
// between position steps, the frames a playing window draws most.
func TestPlayerChromeAllocations(t *testing.T) {
	a, q, fake := playbackApp(&instantResolver{})
	startPlaying(t, a, q, fake)
	fake.Pos = 30.5
	a.playback.Resync()
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
	a.player.TogglePlay()
	paused := allocs()
	t.Logf("allocations per frame: %v playing, %v paused", playing, paused)
	if playing > paused {
		t.Errorf("%v allocations per frame playing, %v paused", playing, paused)
	}
}

func TestEqualizerRestsAwayFromFocus(t *testing.T) {
	a, _ := testApp()
	a.player.Playing = true
	if !a.eqDancing() {
		t.Error("the equalizer rests while playing in the focused window")
	}
	a.playback.SetFocused(false)
	if a.eqDancing() {
		t.Error("the equalizer dances in an unfocused window")
	}
	a.playback.SetFocused(true)
	a.playback.SetShown(false)
	if a.eqDancing() {
		t.Error("the equalizer dances in a hidden window")
	}
	a.playback.SetShown(true)
	a.player.Playing = false
	if a.eqDancing() {
		t.Error("the equalizer dances while paused")
	}
}
