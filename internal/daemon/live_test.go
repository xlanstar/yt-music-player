//go:build audiolive

package daemon

// Live check of the daemon over YouTube Music, signed out, playing on the
// system's output, with sound and network:
// `go test -tags audiolive -run LiveDaemon -v ./internal/daemon/`.
// LIVE_DAEMON_ID picks the song (a video ID).

import (
	"cmp"
	"math"
	"os"
	"testing"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/audio"
	"yt-music-player/internal/auth"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/stream"
	"yt-music-player/internal/ytmusic"
)

func TestLiveDaemonPlays(t *testing.T) {
	svc := New(Options{
		Source:    ytmusic.New(nil, &auth.MemoryStore{}),
		Resolver:  stream.New(stream.Options{YTDLP: "yt-dlp"}),
		NewEngine: func(events func(audio.Event)) (audio.Engine, error) { return audio.New(events, nil) },
	})
	h := &harness{t: t, svc: svc}
	t.Cleanup(h.close)
	svc.Start()
	c := h.connect(true)
	c.event()
	song := catalog.Song{
		ID:    cmp.Or(os.Getenv("LIVE_DAEMON_ID"), "jNQXAC9IVRw"), // "Me at the zoo", 19 s
		Title: "Live check", Artist: "yt-music-player",
	}
	c.call(api.Call{Method: api.MethodSetVolume, Volume: &api.VolumeArgs{Volume: 0.3}})
	c.call(api.Call{Method: api.MethodPlayAll, IDs: &api.IDsArgs{IDs: []string{song.ID}, Songs: []catalog.Song{song}}})
	start := time.Now()
	st := c.liveState(func(st api.State) bool { return st.Playing && !st.Waiting }, 30*time.Second)
	t.Logf("playing after %v: %+v", time.Since(start), st)
	if st.Duration <= 0 {
		t.Errorf("no duration: %+v", st)
	}
	time.Sleep(3 * time.Second)
	status := c.call(api.Call{Method: api.MethodStatus}).Status
	if p := status.State.Position; p < 2.5 || p > 4.5 {
		t.Errorf("3 s in, at %v", p)
	}
	c.call(api.Call{Method: api.MethodSeek, Seek: &api.SeekArgs{Seconds: 8}})
	if st := c.liveState(func(st api.State) bool { return math.Abs(st.Position-8) < 0.5 }, 5*time.Second); !st.Playing {
		t.Errorf("after the seek: %+v", st)
	}
	c.call(api.Call{Method: api.MethodPause})
	paused := c.liveState(func(st api.State) bool { return !st.Playing }, 5*time.Second)
	time.Sleep(time.Second)
	if p := c.call(api.Call{Method: api.MethodStatus}).Status.State.Position; p != paused.Position {
		t.Errorf("paused at %v, then at %v", paused.Position, p)
	}
	c.call(api.Call{Method: api.MethodResume})
	c.call(api.Call{Method: api.MethodSeek, Seek: &api.SeekArgs{Seconds: st.Duration - 2}})
	ended := c.liveState(func(st api.State) bool { return !st.Playing }, 10*time.Second)
	t.Logf("ended: %+v", ended)
}

// liveState waits up to d for a State that ok accepts.
func (c *client) liveState(ok func(api.State) bool, d time.Duration) api.State {
	c.t.Helper()
	deadline := time.After(d)
	for {
		select {
		case f, open := <-c.frames:
			if !open {
				c.t.Fatal("disconnected")
			}
			if f.Event != nil && f.Event.Notice != nil {
				c.t.Fatalf("notice %+v", *f.Event.Notice)
			}
			if f.Event != nil && f.Event.State != nil && ok(*f.Event.State) {
				return *f.Event.State
			}
		case <-deadline:
			c.t.Fatalf("no such state in %v", d)
		}
	}
}
