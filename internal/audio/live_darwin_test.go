//go:build avlive

package audio

// Live check of the AVPlayer engine; it plays sound and uses the network,
// so it never runs with the other tests:
//
//	go test -tags avlive -run Live -v ./internal/audio/
//
// AUDIO_LIVE_URL picks the stream; else yt-dlp resolves AUDIO_LIVE_ID
// (a YouTube video ID) to its audio/mp4 format and headers.

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

	"github.com/ebitengine/purego"
)

// The engine needs the main thread running its run loop, as in a MyGo
// app: the test's main goroutine stays on it and runs it.
func init() { runtime.LockOSThread() }

func TestMain(m *testing.M) {
	const path = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
	coreFoundation, err := purego.Dlopen(path, purego.RTLD_LAZY)
	if err != nil {
		panic(err)
	}
	var run func()
	purego.RegisterLibFunc(&run, coreFoundation, "CFRunLoopRun")
	go func() { os.Exit(m.Run()) }()
	run()
}

type liveEngine struct {
	Engine
	ev chan Event
}

func newLive(t *testing.T) *liveEngine {
	t.Helper()
	events := make(chan Event, 64)
	e, err := New(func(e Event) { events <- e })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return &liveEngine{e, events}
}

// wait fails on Failed or timeout.
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
			if e.Kind == Failed {
				t.Fatalf("failed: %v", e.Err)
			}
		case <-timeout:
			t.Fatalf("no event %d in %v", k, d)
		}
	}
}

func TestLiveLocalFile(t *testing.T) {
	l := newLive(t)
	l.Load(Media{URL: "file:///System/Library/Sounds/Glass.aiff"}, 0)
	l.Play()
	l.wait(t, Ready, 5*time.Second)
	if d := l.Duration(); d <= 0 || d > 5 {
		t.Errorf("duration %v", d)
	}
	// Position is read at every step of the shown position on the main
	// thread, so it should cost no more than reading AVPlayer's time
	// (a CMTime purego returns through reflection).
	e := l.Engine.(*avEngine)
	var wrapped, bare float64
	onMain(func() {
		wrapped = testing.AllocsPerRun(100, func() { e.Position() })
		bare = testing.AllocsPerRun(100, func() { e.position() })
	})
	if wrapped > bare {
		t.Errorf("Position on the main thread allocates %v times, reading the time %v", wrapped, bare)
	}
	l.wait(t, Ended, 5*time.Second)
}

func TestLiveBadURL(t *testing.T) {
	l := newLive(t)
	l.Load(Media{URL: "https://127.0.0.1:9/nothing.m4a"}, 0)
	l.Play()
	timeout := time.After(15 * time.Second)
	for {
		select {
		case e := <-l.ev:
			if e.Kind == Failed {
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
	out, err := exec.CommandContext(ctx, "yt-dlp", "-j", "-f", "140", "--", id).Output()
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
	l.wait(t, Ready, 20*time.Second)
	t.Logf("ready after %v, duration %.2f (stream says %.2f)", time.Since(start), l.Duration(), s.Seconds)
	if s.Seconds > 0 && math.Abs(l.Duration()-s.Seconds) > 1.5 {
		t.Errorf("duration %v, want about %v", l.Duration(), s.Seconds)
	}
	time.Sleep(3 * time.Second)
	if p := l.Position(); p < 3 || p > 6 {
		t.Errorf("3 s after starting at 2, at %v", p)
	}
	l.Seek(10)
	if p := l.Position(); p != 10 {
		t.Errorf("right after seeking to 10, at %v", p)
	}
	time.Sleep(time.Second)
	if p := l.Position(); p < 10 || p > 12 {
		t.Errorf("a second after seeking to 10, at %v", p)
	}
	l.Pause()
	p := l.Position()
	time.Sleep(time.Second)
	if q := l.Position(); q != p {
		t.Errorf("paused, moved from %v to %v", p, q)
	}
	// Two seconds from the end, it must end in about two, not after the
	// song-long silence AVFoundation would play (see halvedEnd).
	l.Seek(l.Duration() - 2)
	l.Play()
	start = time.Now()
	l.wait(t, Ended, 10*time.Second)
	if d := time.Since(start); d > 4*time.Second {
		t.Errorf("ended %v after seeking 2 s from the end", d)
	}
}
