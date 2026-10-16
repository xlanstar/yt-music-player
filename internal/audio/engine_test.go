package audio

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	mygoaudio "github.com/egoist/mygo/audio"
)

// fakeOutput plays nothing: as MyGo's player, it decodes ahead on a
// goroutine of its own, and its position is the test's to move.
type fakeOutput struct {
	d mygoaudio.Decoder

	mu       sync.Mutex
	playing  bool
	ended    bool
	pos      time.Duration
	volume   float64
	seeks    []time.Duration
	isClosed bool
	frames   int64 // decoded
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
}

type engineFixture struct {
	*engine
	ev   chan Event
	mu   sync.Mutex
	outs []*fakeOutput
}

func newEngineFixture(t *testing.T) *engineFixture {
	t.Helper()
	x := &engineFixture{ev: make(chan Event, 64)}
	x.engine = newEngine(func(ev Event) { x.ev <- ev }, http.DefaultClient, func(d mygoaudio.Decoder) output {
		o := &fakeOutput{d: d, volume: 1}
		go o.decode()
		x.mu.Lock()
		x.outs = append(x.outs, o)
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
	srv := serveTone(t)
	x := newEngineFixture(t)
	x.SetVolume(0.4)
	x.Load(Media{URL: srv.URL}, 2.5)
	if p := x.Position(); p != 2.5 {
		t.Errorf("before ready, at %v", p)
	}
	x.Play()
	x.expect(t, Ready, 1)
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
}

func TestEngineEnds(t *testing.T) {
	srv := serveTone(t)
	x := newEngineFixture(t)
	x.Load(Media{URL: srv.URL}, 0)
	x.expect(t, Ready, 1)
	x.Seek(5.9)
	x.Play()
	x.out().end()
	x.expect(t, Ended, 1)
	// The end pauses: Play starts it again.
	x.Seek(0)
	x.Play()
	if !x.out().Playing() {
		t.Error("not playing again")
	}
}

func TestEngineFails(t *testing.T) {
	retryWait = time.Millisecond
	t.Cleanup(func() { retryWait = 500 * time.Millisecond })
	expired := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(expired.Close)
	notWebM := serveBytes(t, []byte("ID3\x04\x00 not a WebM file at all"))
	for _, tc := range []struct{ name, url string }{
		{"bad URL", "file:///etc/passwd"},
		{"expired", expired.URL},
		{"not WebM", notWebM.URL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := newEngineFixture(t)
			x.Load(Media{URL: tc.url}, 0)
			x.Play()
			if ev := x.expect(t, Failed, 1); ev.Err == nil {
				t.Error("no error")
			}
		})
	}
}

func TestEngineFailsMidSong(t *testing.T) {
	data := readFixture(t, toneFixtures[1])
	srv := serveBytes(t, data[:len(data)/2])
	x := newEngineFixture(t)
	x.Load(Media{URL: srv.URL}, 0)
	x.Play()
	x.expect(t, Ready, 1)
	if ev := x.expect(t, Failed, 1); !errors.Is(ev.Err, io.ErrUnexpectedEOF) {
		t.Errorf("err %v", ev.Err)
	}
}

func TestEngineDropsTheLoadItReplaced(t *testing.T) {
	block := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-block }))
	t.Cleanup(func() {
		close(block)
		slow.Close()
	})
	srv := serveTone(t)
	x := newEngineFixture(t)
	x.Load(Media{URL: slow.URL}, 0)
	x.Play()
	x.Load(Media{URL: srv.URL}, 0)
	x.expect(t, Ready, 2)
	x.none(t)
}

// stallServer sends the file's first bytes, then waits for release before
// the rest.
func stallServer(t *testing.T, data []byte, first int) (*httptest.Server, chan struct{}) {
	t.Helper()
	release := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	}))
	t.Cleanup(srv.Close)
	return srv, release
}

func TestEngineBuffers(t *testing.T) {
	data := readFixture(t, toneFixtures[1])
	// The header and a few packets: less than stallMargin.
	srv, release := stallServer(t, data, 1000)
	x := newEngineFixture(t)
	x.Load(Media{URL: srv.URL}, 0)
	x.Play()
	x.expect(t, Ready, 1)
	x.expect(t, Buffering, 1)
	close(release)
	x.expect(t, Playing, 1)
	x.none(t)
}

func TestEngineIgnoresShortWaits(t *testing.T) {
	data := readFixture(t, toneFixtures[1])
	srv, release := stallServer(t, data, 1000)
	x := newEngineFixture(t)
	x.Load(Media{URL: srv.URL}, 0)
	x.Play()
	x.expect(t, Ready, 1)
	time.AfterFunc(stallGrace/4, func() { close(release) })
	x.none(t)
	time.Sleep(stallGrace)
	x.none(t)
}

func TestEngineIgnoresStallsWhilePaused(t *testing.T) {
	data := readFixture(t, toneFixtures[1])
	srv, release := stallServer(t, data, 1000)
	x := newEngineFixture(t)
	x.Load(Media{URL: srv.URL}, 0)
	x.expect(t, Ready, 1)
	x.none(t)
	close(release)
	x.none(t)
}

func TestEngineCloses(t *testing.T) {
	srv := serveTone(t)
	x := newEngineFixture(t)
	x.Load(Media{URL: srv.URL}, 0)
	x.expect(t, Ready, 1)
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
}

func TestEnginePassesCommandsOn(t *testing.T) {
	x := newEngineFixture(t)
	x.command(Event{Kind: CommandNext})
	if ev := x.next(t); ev.Kind != CommandNext {
		t.Errorf("event %+v", ev)
	}
	x.Close()
	x.command(Event{Kind: CommandNext})
	x.none(t)
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

// await waits for the controls to show what ok wants: the engine publishes
// after sending its event.
func (c *recordingControls) await(t *testing.T, ok func(nowPlaying) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok(c.get()) {
		if time.Now().After(deadline) {
			t.Fatalf("shows %+v", c.get())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestEnginePublishes(t *testing.T) {
	srv := serveTone(t)
	x := newEngineFixture(t)
	c := &recordingControls{}
	x.controls = c
	info := Info{Title: "Tone", Artist: "ffmpeg", Seconds: 7}
	x.SetInfo(info)
	x.Load(Media{URL: srv.URL}, 1)
	if np := c.get(); np.state != statePaused || np.duration != 7 || np.elapsed != 1 || np.advancing {
		t.Errorf("loading: %+v", np)
	}
	x.Play()
	if np := c.get(); np.state != statePlaying || np.advancing {
		t.Errorf("playing before ready: %+v", np)
	}
	x.expect(t, Ready, 1)
	c.await(t, func(np nowPlaying) bool {
		return np.Info == info && np.state == statePlaying && np.advancing && np.duration > 6 && np.duration < 6.05
	})
	x.Close()
	if np := c.get(); np.state != stateStopped || !c.isClosed || c.claims != 1 {
		t.Errorf("closed: %+v, closed %v, %d claims", np, c.isClosed, c.claims)
	}
}
