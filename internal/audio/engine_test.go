package audio

import (
	"bytes"
	"cmp"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
	"weak"

	mygoaudio "github.com/egoist/mygo/audio"
)

// fakeOutput plays nothing: as MyGo's player, it decodes ahead on a
// goroutine of its own, and its position is the test's to move.
type fakeOutput struct {
	d mygoaudio.Decoder

	mu       sync.Mutex
	pos      time.Duration
	volume   float64
	seeks    []time.Duration
	frames   int64 // decoded
	endFn    func()
	playing  bool
	ended    bool
	isClosed bool
}

func (o *fakeOutput) decode() {
	buf := make([]float32, 2048*2)
	for {
		n, err := o.d.Read(buf)
		o.mu.Lock()
		o.frames += int64(n / 2)
		closed := o.isClosed
		o.mu.Unlock()
		if err != nil || closed {
			return
		}
	}
}

func (o *fakeOutput) Play() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.playing, o.ended = true, false
}

func (o *fakeOutput) Pause() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.playing = false
}

func (o *fakeOutput) Playing() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.playing
}

func (o *fakeOutput) Seek(t time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.pos = t
	o.seeks = append(o.seeks, t)
}

func (o *fakeOutput) Position() time.Duration {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.pos
}

func (o *fakeOutput) Duration() time.Duration {
	if n := o.d.Len(); n > 0 {
		return time.Duration(n) * time.Second / opusRate
	}
	return 0
}

func (o *fakeOutput) setVolume(v float64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.volume = v
}

func (o *fakeOutput) onEnd(fn func()) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.endFn = fn
}

func (o *fakeOutput) Close() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.isClosed = true
}

// end plays the output to its end, as MyGo's does.
func (o *fakeOutput) end() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.pos, o.playing, o.ended = o.Duration(), false, true
	if o.endFn != nil {
		go o.endFn()
	}
}

type engineFixture struct {
	*engine
	ev   chan Event
	mu   sync.Mutex
	outs []*fakeOutput
	// lastSrc is the source of the last output made.
	lastSrc *httpSource
}

// newEngineFixture makes an engine fetching through client, a test
// server's, or nil for one that fetches nothing.
func newEngineFixture(t *testing.T, client *http.Client) *engineFixture {
	t.Helper()
	x := &engineFixture{ev: make(chan Event, 64)}
	x.engine = newEngine(func(ev Event) { x.ev <- ev }, client, func(d mygoaudio.Decoder) output {
		o := &fakeOutput{d: d, volume: 1}
		go o.decode()
		x.mu.Lock()
		x.outs = append(x.outs, o)
		x.lastSrc = d.(*opusStream).src.(*httpSource)
		x.mu.Unlock()
		return o
	})
	t.Cleanup(x.Close)
	return x
}

func (x *engineFixture) out() *fakeOutput {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.outs[len(x.outs)-1]
}

// next waits for the next event.
func (x *engineFixture) next(t *testing.T) Event {
	t.Helper()
	select {
	case ev := <-x.ev:
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
		return Event{}
	}
}

func (x *engineFixture) expect(t *testing.T, k Kind, load uint64) Event {
	t.Helper()
	ev := x.next(t)
	if ev.Kind != k || ev.Load != load {
		t.Fatalf("event %+v, want kind %d of load %d", ev, k, load)
	}
	return ev
}

// none checks that no event comes for a while.
func (x *engineFixture) none(t *testing.T) {
	t.Helper()
	select {
	case ev := <-x.ev:
		t.Fatalf("unexpected event %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

func serveTone(t *testing.T) *rangeServer {
	t.Helper()
	return serveBytes(t, readFixture(t, toneFixtures[1]))
}

func TestEngineLoadsAndPlays(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := serveTone(t)
		x := newEngineFixture(t, srv.Client())
		x.SetVolume(0.4)
		x.Load(Media{URL: srv.URL}, 2.5)
		if p := x.Position(); p != 2.5 {
			t.Errorf("before ready, at %v", p)
		}
		x.Play()
		x.expect(t, StreamReady, 1)
		o := x.out()
		if !o.Playing() || o.volume != 0.4 || len(o.seeks) != 1 || o.seeks[0] != 2500*time.Millisecond {
			t.Errorf("output playing %v, volume %v, seeks %v", o.Playing(), o.volume, o.seeks)
		}
		if d := x.Duration(); d < 6 || d > 6.05 {
			t.Errorf("duration %v", d)
		}
		x.Pause()
		if o.Playing() {
			t.Error("still playing after Pause")
		}
		x.Seek(4)
		if p := x.Position(); p != 4 {
			t.Errorf("after Seek(4), at %v", p)
		}
		x.SetVolume(2)
		if o.volume != 1 {
			t.Errorf("volume %v, want it held at 1", o.volume)
		}
		x.none(t)
	})
}

func TestEngineEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := serveTone(t)
		x := newEngineFixture(t, srv.Client())
		x.Load(Media{URL: srv.URL}, 0)
		x.expect(t, StreamReady, 1)
		x.Seek(5.9)
		x.Play()
		x.out().end()
		x.expect(t, StreamEnded, 1)
		// The end pauses: Play starts it again.
		x.Seek(0)
		x.Play()
		if !x.out().Playing() {
			t.Error("not playing again")
		}
	})
}

func TestEngineIgnoresEndsWhilePaused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := serveTone(t)
		x := newEngineFixture(t, srv.Client())
		x.Load(Media{URL: srv.URL}, 0)
		x.expect(t, StreamReady, 1)
		x.Play()
		x.Pause()
		// Mixed to its end before the pause was heard.
		x.out().end()
		x.none(t)
	})
}

func TestEngineFails(t *testing.T) {
	expired := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }
	notWebM := func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "", time.Time{}, strings.NewReader("ID3\x04\x00 not a WebM file at all"))
	}
	for _, tc := range []struct {
		name  string
		serve http.HandlerFunc
		url   string // the server's when empty
	}{
		{"bad URL", nil, "file:///etc/passwd"},
		{"expired", expired, ""},
		{"not WebM", notWebM, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				srv := startServer(t, tc.serve)
				x := newEngineFixture(t, srv.Client())
				x.Load(Media{URL: cmp.Or(tc.url, srv.URL)}, 0)
				x.Play()
				if ev := x.expect(t, StreamFailed, 1); ev.Err == nil {
					t.Error("no error")
				}
			})
		})
	}
}

func TestEngineFailsMidSong(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		data := readFixture(t, toneFixtures[1])
		srv := serveBytes(t, data[:len(data)/2])
		x := newEngineFixture(t, srv.Client())
		x.Load(Media{URL: srv.URL}, 0)
		x.Play()
		x.expect(t, StreamReady, 1)
		if ev := x.expect(t, StreamFailed, 1); !errors.Is(ev.Err, io.ErrUnexpectedEOF) {
			t.Errorf("err %v", ev.Err)
		}
	})
}

func TestEngineDropsTheLoadItReplaced(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tone := readFixture(t, toneFixtures[1])
		block := make(chan struct{})
		srv := startServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/slow" {
				<-block
				return
			}
			http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(tone))
		})
		t.Cleanup(func() { close(block) })
		x := newEngineFixture(t, srv.Client())
		x.Load(Media{URL: srv.URL + "/slow"}, 0)
		x.Play()
		x.Load(Media{URL: srv.URL}, 0)
		x.expect(t, StreamReady, 2)
		x.none(t)
	})
}

// TestEngineFreesWhatItDrops plays through MyGo's output, which opens no
// device until Play: a stream dropped must be collected, its decoder too.
func TestEngineFreesWhatItDrops(t *testing.T) {
	for _, tc := range []struct {
		name string
		drop func(e *engine, url string)
	}{
		{"Load", func(e *engine, url string) { e.Load(Media{URL: url}, 0) }},
		{"Release", func(e *engine, _ string) { e.Release() }},
		{"Close", func(e *engine, _ string) { e.Close() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				srv := serveTone(t)
				type made struct {
					player weak.Pointer[mygoaudio.Player]
					stream weak.Pointer[opusStream]
				}
				outs := make(chan made, 2)
				ready := make(chan Event, 4)
				e := newEngine(func(ev Event) { ready <- ev }, srv.Client(), func(d mygoaudio.Decoder) output {
					out := newMyGoOutput(d)
					outs <- made{weak.Make(out.(mygoOutput).Player), weak.Make(d.(*opusStream))}
					return out
				})
				t.Cleanup(e.Close)
				e.Load(Media{URL: srv.URL}, 0)
				if ev := <-ready; ev.Kind != StreamReady {
					t.Fatalf("event %+v", ev)
				}
				first := <-outs
				tc.drop(e, srv.URL)
				// MyGo's cleanup frees the decoder a GC after the one that
				// finds the player gone.
				for i := 0; first.player.Value() != nil || first.stream.Value() != nil; i++ {
					if i == 100 {
						t.Fatalf("kept: player %v, stream %v", first.player.Value() != nil, first.stream.Value() != nil)
					}
					runtime.GC()
				}
			})
		})
	}
}

// stallServer sends the file's first bytes, then waits for release before
// the rest.
func stallServer(t *testing.T, data []byte, first int) (*httptest.Server, chan struct{}) {
	t.Helper()
	release := make(chan struct{})
	var once sync.Once
	srv := startServer(t, func(w http.ResponseWriter, r *http.Request) {
		stalled := false
		once.Do(func() { stalled = true })
		if !stalled {
			http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
			return
		}
		w.Header().Set("Content-Range", "bytes 0-"+strconv.Itoa(len(data)-1)+"/"+strconv.Itoa(len(data)))
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(data[:first])
		w.(http.Flusher).Flush()
		<-release
		_, _ = w.Write(data[first:])
	})
	return srv, release
}

func TestEngineBuffers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		data := readFixture(t, toneFixtures[1])
		// The header and a few packets: less than stallMargin.
		srv, release := stallServer(t, data, 1000)
		x := newEngineFixture(t, srv.Client())
		x.Load(Media{URL: srv.URL}, 0)
		x.Play()
		x.expect(t, StreamReady, 1)
		x.expect(t, StreamBuffering, 1)
		close(release)
		x.expect(t, StreamPlaying, 1)
		x.none(t)
	})
}

func TestEngineIgnoresShortWaits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		data := readFixture(t, toneFixtures[1])
		srv, release := stallServer(t, data, 1000)
		x := newEngineFixture(t, srv.Client())
		x.Load(Media{URL: srv.URL}, 0)
		x.Play()
		x.expect(t, StreamReady, 1)
		time.AfterFunc(stallGrace/4, func() { close(release) })
		x.none(t)
		time.Sleep(stallGrace)
		x.none(t)
	})
}

func TestEngineIgnoresStallsWhilePaused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		data := readFixture(t, toneFixtures[1])
		srv, release := stallServer(t, data, 1000)
		x := newEngineFixture(t, srv.Client())
		x.Load(Media{URL: srv.URL}, 0)
		x.expect(t, StreamReady, 1)
		x.none(t)
		close(release)
		x.none(t)
	})
}

func TestEngineCloses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := serveTone(t)
		x := newEngineFixture(t, srv.Client())
		x.Load(Media{URL: srv.URL}, 0)
		x.expect(t, StreamReady, 1)
		x.Close()
		if !x.out().isClosed {
			t.Error("output left open")
		}
		x.Close()
		x.Load(Media{URL: srv.URL}, 0)
		x.Play()
		x.none(t)
		if p, d := x.Position(), x.Duration(); p != 0 || d != 0 {
			t.Errorf("closed: at %v of %v", p, d)
		}
	})
}

func TestEnginePassesCommandsOn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		x := newEngineFixture(t, nil)
		x.command(Event{Kind: CommandNext})
		if ev := x.next(t); ev.Kind != CommandNext {
			t.Errorf("event %+v", ev)
		}
		x.Close()
		x.command(Event{Kind: CommandNext})
		x.none(t)
	})
}

// recordingControls keep what the engine publishes.
type recordingControls struct {
	mu       sync.Mutex
	claims   int
	last     nowPlaying
	isClosed bool
}

func (c *recordingControls) claim() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.claims++
}

func (c *recordingControls) publish(np nowPlaying) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.last = np
}

func (c *recordingControls) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.isClosed = true
}

func (c *recordingControls) get() nowPlaying {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

// await checks the controls show what ok wants once the bubble settles:
// the engine publishes after sending its event.
func (c *recordingControls) await(t *testing.T, ok func(nowPlaying) bool) {
	t.Helper()
	synctest.Wait()
	if np := c.get(); !ok(np) {
		t.Fatalf("shows %+v", np)
	}
}

func TestEnginePublishes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := serveTone(t)
		x := newEngineFixture(t, srv.Client())
		c := &recordingControls{}
		x.controls = c
		info := Info{Title: "Tone", Artist: "ffmpeg", Seconds: 7}
		x.SetInfo(info)
		x.Load(Media{URL: srv.URL}, 1)
		if np := c.get(); np.state != statePaused || np.duration != 7 || np.elapsed != 1 || np.isAdvancing {
			t.Errorf("loading: %+v", np)
		}
		x.Play()
		if np := c.get(); np.state != statePlaying || np.isAdvancing {
			t.Errorf("playing before ready: %+v", np)
		}
		x.expect(t, StreamReady, 1)
		c.await(t, func(np nowPlaying) bool {
			return np.Info == info && np.state == statePlaying && np.isAdvancing && np.duration > 6 && np.duration < 6.05
		})
		x.Close()
		if np := c.get(); np.state != stateStopped || !c.isClosed || c.claims != 1 {
			t.Errorf("closed: %+v, closed %v, %d claims", np, c.isClosed, c.claims)
		}
	})
}

func TestEngineReleases(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := serveTone(t)
		x := newEngineFixture(t, srv.Client())
		c := &recordingControls{}
		x.controls = c
		x.Load(Media{URL: srv.URL}, 0)
		x.Play()
		x.expect(t, StreamReady, 1)
		o := x.out()
		o.Seek(3 * time.Second)
		// A stream that plays is kept.
		x.Release()
		if o.isClosed {
			t.Fatal("released while playing")
		}
		x.Pause()
		x.Release()
		if !o.isClosed {
			t.Error("output left open")
		}
		x.src().mu.Lock()
		isClosed := x.src().isClosed
		x.src().mu.Unlock()
		if !isClosed {
			t.Error("source left open")
		}
		if p, d := x.Position(), x.Duration(); p != 3 || d < 6 || d > 6.05 {
			t.Errorf("released: at %v of %v, want 3 of 6", p, d)
		}
		if np := c.get(); np.state != statePaused || np.elapsed != 3 || np.duration < 6 || np.isAdvancing {
			t.Errorf("released: shows %+v", np)
		}
		x.Seek(4)
		x.Play()
		if np := c.get(); np.state != statePaused || np.elapsed != 4 {
			t.Errorf("released, sought and played: shows %+v", np)
		}
		x.none(t)
		// A Load plays on.
		x.Load(Media{URL: srv.URL}, x.Position())
		x.Play()
		x.expect(t, StreamReady, 2)
		if o := x.out(); !o.Playing() || len(o.seeks) != 1 || o.seeks[0] != 4*time.Second {
			t.Errorf("loaded again: playing %v, seeks %v", o.Playing(), o.seeks)
		}
	})
}

// src is the current load's source.
func (x *engineFixture) src() *httpSource {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.lastSrc
}

func TestSeconds(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want time.Duration
	}{
		{2.5, 2500 * time.Millisecond},
		{0, 0},
		{-1, -time.Second},
		{math.NaN(), 0},
		{1e300, math.MaxInt64},
		{math.Inf(1), math.MaxInt64},
		{-1e300, math.MinInt64},
		{math.Inf(-1), math.MinInt64},
	} {
		if got := seconds(tc.in); got != tc.want {
			t.Errorf("seconds(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
