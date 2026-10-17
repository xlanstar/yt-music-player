package main

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
)

var update = flag.Bool("update", false, "rewrite testdata/status.json")

// playingStatus is a daemon's status while it plays.
func playingStatus() api.Status {
	return api.Status{
		Version: "0.3.0",
		Proto:   api.Proto,
		State: api.State{
			Playing: true, Position: 81.5, Duration: 213, Volume: 0.7,
			Repeat: api.RepeatOff, Current: "dQw4w9WgXcQ", Index: 3,
		},
		Song: catalog.Song{
			ID: "dQw4w9WgXcQ", Title: "Paper Moons", Artist: "Ivy Calder",
			Album: "Night Swim", AlbumID: "MPREb_x", Seconds: 213,
		},
		Length:   42,
		SignedIn: true,
		Account:  catalog.Account{Name: "Lan"},
	}
}

// TestStatusJSON holds the fields scripts read.
func TestStatusJSON(t *testing.T) {
	d := newDaemon("0.3.0", 7)
	d.status = playingStatus()
	w := newWorld(d)
	if code := w.run(t, "status", "--json"); code != exitOK {
		t.Fatalf("exit %d; stderr %q", code, w.stderr)
	}
	golden := filepath.Join("testdata", "status.json")
	if *update {
		if err := os.WriteFile(golden, w.stdout.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got := w.stdout.String(); got != string(want) {
		t.Errorf("status --json:\n%s\nwant:\n%s", got, want)
	}
}

func TestStatusJSONShapes(t *testing.T) {
	idle := newDaemon("0.3.0", 7)
	idle.status = api.Status{Version: "0.3.0", Proto: api.Proto, State: api.State{Volume: 1, Repeat: api.RepeatAll}}
	incompatible := newDaemon("2.0.0", 7)
	incompatible.hello.Proto = api.Proto + 1
	for _, tc := range []struct {
		name   string
		daemon *daemon
		code   int
		want   string
	}{
		{"stopped", nil, exitStopped, `{"running":false}` + "\n"},
		{"incompatible", incompatible, exitFailed, `{"running":true,"version":"2.0.0","proto":2,"compatible":false}` + "\n"},
		{
			"idle", idle, exitOK,
			`{"running":true,"version":"0.3.0","proto":1,"compatible":true,"state":"idle","position":0,"duration":0,"volume":100,"muted":false,"repeat":"all","shuffle":false,"song":null,"queue":{"index":0,"length":0},"account":{"signedIn":false,"name":""}}` + "\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var w *world
			if tc.daemon == nil {
				w = newWorld(nil, newDaemon("1.0.0", 8))
			} else {
				w = newWorld(tc.daemon)
			}
			if code := w.run(t, "status", "--json"); code != tc.code {
				t.Errorf("exit %d, want %d", code, tc.code)
			}
			if got := w.stdout.String(); got != tc.want {
				t.Errorf("stdout %s, want %s", got, tc.want)
			}
		})
	}
}

func TestStatusText(t *testing.T) {
	playing := playingStatus()
	paused := playingStatus()
	paused.State.Playing, paused.State.Muted, paused.State.Shuffled = false, true, true
	paused.State.Repeat = api.RepeatOne
	paused.State.Duration, paused.State.Position = 3725, 3601
	for _, tc := range []struct {
		name   string
		status api.Status
		want   string
	}{
		{"playing", playing, "Now playing: Paper Moons by Ivy Calder\n" +
			"  1:21 of 3:33 · song 4 of 42\n" +
			"Volume 70% · Repeat off · Shuffle off\n" +
			"Signed in as Lan\n"},
		{"paused", paused, "Paused: Paper Moons by Ivy Calder\n" +
			"  1:00:01 of 1:02:05 · song 4 of 42\n" +
			"Volume 70% (muted) · Repeat one · Shuffle on\n" +
			"Signed in as Lan\n"},
		{"idle", api.Status{State: api.State{Volume: 0.25}}, "Nothing is playing: the queue is empty.\n" +
			"Volume 25% · Repeat off · Shuffle off\n" +
			"Not signed in\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newDaemon("1.0.0", 7)
			d.status = tc.status
			w := newWorld(d)
			if code := w.run(t, "status"); code != exitOK {
				t.Fatalf("exit %d; stderr %q", code, w.stderr)
			}
			if got := w.stdout.String(); got != tc.want {
				t.Errorf("status:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}
