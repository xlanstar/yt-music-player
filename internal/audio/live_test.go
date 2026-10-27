//go:build audiolive

package audio

// Live check of the engine on the system's output, with sound and network:
// `go test -tags audiolive -run Live -v ./internal/audio/`. AUDIO_LIVE_URL
// picks the stream; else yt-dlp resolves AUDIO_LIVE_ID (a YouTube video ID)
// to its Opus format and headers.

import (
	"context"
	"encoding/json/v2"
	"math"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"

	mygoaudio "github.com/egoist/mygo/audio"
)

type liveEngine struct {
	Engine
	ev chan Event
}

func newLive(t *testing.T) *liveEngine {
	t.Helper()
	events := make(chan Event, 64)
	e, err := New(func(e Event) { events <- e }, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return &liveEngine{e, events}
}

// wait fails on StreamFailed or timeout.
func (l *liveEngine) wait(t *testing.T, k Kind, d time.Duration) {
	t.Helper()
	timeout := time.After(d)
	for {
		select {
		case e := <-l.ev:
			t.Logf("event %d (load %d) at %.2fs", e.Kind, e.Load, l.Position())
			if e.Kind == k {
				return
			}
			if e.Kind == StreamFailed {
				t.Fatalf("failed: %v", e.Err)
			}
		case <-timeout:
			t.Fatalf("no event %d in %v", k, d)
		}
	}
}

func TestLiveBadURL(t *testing.T) {
	l := newLive(t)
	l.Load(Media{URL: "https://127.0.0.1:9/nothing.webm"}, 0)
	l.Play()
	timeout := time.After(30 * time.Second)
	for {
		select {
		case e := <-l.ev:
			if e.Kind == StreamFailed {
				t.Logf("failed as expected: %v", e.Err)
				return
			}
		case <-timeout:
			t.Fatal("no failure")
		}
	}
}

func liveStream(t *testing.T) Media {
	if u := os.Getenv("AUDIO_LIVE_URL"); u != "" {
		return Media{URL: u}
	}
	id := os.Getenv("AUDIO_LIVE_ID")
	if id == "" {
		id = "jNQXAC9IVRw" // "Me at the zoo", 19 s
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "yt-dlp", "-j", "-f", "251", "--", id).Output()
	if err != nil {
		t.Skipf("yt-dlp: %v", err)
	}
	var v struct {
		URL         string            `json:"url"`
		Duration    float64           `json:"duration"`
		HTTPHeaders map[string]string `json:"http_headers"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatal(err)
	}
	header := http.Header{}
	for k, value := range v.HTTPHeaders {
		header.Set(k, value)
	}
	return Media{URL: v.URL, Seconds: v.Duration, Header: header}
}

func TestLiveStream(t *testing.T) {
	s := liveStream(t)
	l := newLive(t)
	l.SetVolume(0.3)
	l.SetInfo(Info{Title: "Live check", Artist: "yt-music-player", Seconds: s.Seconds})
	start := time.Now()
	l.Load(s, 2)
	l.Play()
	l.wait(t, StreamReady, 20*time.Second)
	t.Logf("ready after %v, duration %.2f (stream says %.2f)", time.Since(start), l.Duration(), s.Seconds)
	if s.Seconds > 0 && math.Abs(l.Duration()-s.Seconds) > 1.5 {
		t.Errorf("duration %v, want about %v", l.Duration(), s.Seconds)
	}
	time.Sleep(3 * time.Second)
	if err := mygoaudio.Err(); err != nil {
		t.Logf("no audio output, played unheard: %v", err)
	}
	if p := l.Position(); p < 3.5 || p > 5.5 {
		t.Errorf("3 s after starting at 2, at %v", p)
	}
	l.Seek(10)
	if p := l.Position(); math.Abs(p-10) > 0.05 {
		t.Errorf("right after seeking to 10, at %v", p)
	}
	time.Sleep(time.Second)
	if p := l.Position(); p < 10.3 || p > 11.5 {
		t.Errorf("a second after seeking to 10, at %v", p)
	}
	l.Pause()
	time.Sleep(300 * time.Millisecond) // what the output holds plays out (LatencyPlayback)
	p := l.Position()
	time.Sleep(time.Second)
	if q := l.Position(); q != p {
		t.Errorf("paused, moved from %v to %v", p, q)
	}
	l.Seek(l.Duration() - 2)
	l.Play()
	start = time.Now()
	l.wait(t, StreamEnded, 10*time.Second)
	if d := time.Since(start); d < 1500*time.Millisecond || d > 3500*time.Millisecond {
		t.Errorf("ended %v after seeking 2 s from the end", d)
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	t.Logf("heap in use %.1f MB", float64(m.HeapInuse)/(1<<20))
}
