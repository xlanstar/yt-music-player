package main

import (
	"slices"
	"strings"
	"testing"
	"yt-music-player/internal/api"
)

// TestSpawn checks which commands start ytmd when none runs
// (docs/daemon.md), and their exit codes then.
func TestSpawn(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		spawns int
		code   int
		// call is the last method the started daemon got, 0 for none.
		call api.Method
	}{
		{[]string{"status"}, 0, exitStopped, 0},
		{[]string{"status", "--json"}, 0, exitStopped, 0},
		{[]string{"daemon", "status"}, 0, exitStopped, 0},
		{[]string{"daemon", "stop"}, 0, exitOK, 0},
		{[]string{"version"}, 0, exitOK, 0},
		{[]string{"logs"}, 0, exitOK, 0},
		{[]string{"autostart", "status"}, 0, exitOK, 0},
		{[]string{"help"}, 0, exitOK, 0},
		{[]string{"play"}, 1, exitOK, api.MethodResume},
		{[]string{"pause"}, 1, exitOK, api.MethodPause},
		{[]string{"toggle"}, 1, exitOK, api.MethodToggle},
		{[]string{"next"}, 1, exitOK, api.MethodNext},
		{[]string{"prev"}, 1, exitOK, api.MethodPrev},
		{[]string{"seek", "30"}, 1, exitOK, api.MethodSeek},
		{[]string{"volume", "+5"}, 1, exitOK, api.MethodSetVolume},
		{[]string{"login"}, 1, exitOK, api.MethodSignIn},
		{[]string{"logout"}, 1, exitOK, api.MethodSignOut},
		{[]string{"daemon", "start"}, 1, exitOK, 0},
		{[]string{"daemon", "restart"}, 1, exitOK, 0},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			started := newDaemon("1.0.0", 7)
			w := newWorld(nil, started)
			w.env.stdin = strings.NewReader("SID=abc; SAPISID=def\n")
			if code := w.run(t, tc.args...); code != tc.code {
				t.Errorf("exit %d, want %d; stderr %q", code, tc.code, w.stderr)
			}
			if got := w.dialer.spawned(); got != tc.spawns {
				t.Errorf("%d spawns, want %d", got, tc.spawns)
			}
			if tc.call != 0 {
				if got := started.last(t).Method; got != tc.call {
					t.Errorf("called %v, want %v", got, tc.call)
				}
			}
		})
	}
}

// TestRunning checks that no command starts ytmd while one runs.
func TestRunning(t *testing.T) {
	for _, args := range [][]string{
		{"status"},
		{"next"},
		{"seek", "-5"},
		{"volume", "40"},
		{"login"},
		{"logout"},
		{"daemon", "start"},
		{"daemon", "status"},
		{"version"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			w := newWorld(newDaemon("1.0.0", 7), newDaemon("1.0.0", 8))
			w.env.stdin = strings.NewReader("SID=abc")
			if code := w.run(t, args...); code != exitOK {
				t.Errorf("exit %d; stderr %q", code, w.stderr)
			}
			if n := w.dialer.spawned(); n != 0 {
				t.Errorf("%d spawns", n)
			}
		})
	}
}

func TestUsage(t *testing.T) {
	for _, args := range [][]string{
		{"bogus"},
		{"status", "--yaml"},
		{"status", "--json", "now"},
		{"play", "now"},
		{"seek"},
		{"seek", "soon"},
		{"seek", "-"},
		{"seek", "NaN"},
		{"volume", "101"},
		{"volume", "Inf"},
		{"volume", "1", "2"},
		{"login", "--cookie", "x"},
		{"daemon"},
		{"daemon", "reload"},
		{"autostart", "maybe"},
		{"logs", "all"},
		{"version", "--short"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			w := newWorld(nil, newDaemon("1.0.0", 7))
			if code := w.run(t, args...); code != exitUsage {
				t.Errorf("exit %d, want %d", code, exitUsage)
			}
			if w.stderr.Len() == 0 {
				t.Error("nothing told")
			}
			if n := w.dialer.spawned(); n != 0 {
				t.Errorf("%d spawns", n)
			}
		})
	}
}

// helpText is what ytm help writes, in English.
const helpText = `Control YT Music Player from the terminal. Commands:

  ytm                                   Open the player in the terminal
  ytm status [--json]                   Show what is playing
  ytm play | pause | toggle             Play, pause, or switch between them
  ytm next | prev                       Skip to the next or the previous song
  ytm seek <sec|+sec|-sec>              Go to a time in the song, or forward or back by seconds
  ytm volume <0-100|+n|-n>              Set the volume, or turn it up or down
  ytm login                             Sign in with a Cookie header, pasted or piped in
  ytm logout                            Sign out
  ytm daemon start|stop|restart|status  Start, stop or restart the background player
  ytm autostart on|off|status           Start the background player when you log in, or not
  ytm logs                              Show where the log files are
  ytm version                           Show the versions of ytm and the background player
  ytm help                              Show this list
`

func TestHelp(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}, {"help", "seek"}} {
		w := newWorld(nil)
		if code := w.run(t, args...); code != exitOK {
			t.Errorf("%s: exit %d", args, code)
		}
		if got := w.stdout.String(); got != helpText {
			t.Errorf("%s: wrote %q, want %q", args, got, helpText)
		}
	}
}

// TestUsageText checks each command's usage, told on stderr when misused.
func TestUsageText(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"status", "x"}, "Usage: ytm status [--json]\n"},
		{[]string{"play", "x"}, "Usage: ytm play\n"},
		{[]string{"pause", "x"}, "Usage: ytm pause\n"},
		{[]string{"toggle", "x"}, "Usage: ytm toggle\n"},
		{[]string{"next", "x"}, "Usage: ytm next\n"},
		{[]string{"prev", "x"}, "Usage: ytm prev\n"},
		{[]string{"seek"}, "Usage: ytm seek <sec|+sec|-sec>\n"},
		{[]string{"volume"}, "Usage: ytm volume <0-100|+n|-n>\n"},
		{[]string{"login", "x"}, "Usage: ytm login\n"},
		{[]string{"logout", "x"}, "Usage: ytm logout\n"},
		{[]string{"daemon"}, "Usage: ytm daemon start|stop|restart|status\n"},
		{[]string{"autostart"}, "Usage: ytm autostart on|off|status\n"},
		{[]string{"logs", "x"}, "Usage: ytm logs\n"},
		{[]string{"version", "x"}, "Usage: ytm version\n"},
		{[]string{"bogus"}, "Unknown command “bogus”. Run “ytm help” for the commands.\n"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			w := newWorld(nil)
			if code := w.run(t, tc.args...); code != exitUsage {
				t.Errorf("exit %d, want %d", code, exitUsage)
			}
			if got := w.stderr.String(); got != tc.want {
				t.Errorf("stderr %q, want %q", got, tc.want)
			}
		})
	}
}

// TestNotFound checks that a control fails when ytmd cannot start.
func TestNotFound(t *testing.T) {
	w := newWorld(nil)
	if code := w.run(t, "next"); code != exitFailed {
		t.Errorf("exit %d, want %d", code, exitFailed)
	}
	if !strings.Contains(w.stderr.String(), "YTMD_PATH") {
		t.Errorf("stderr %q does not tell of YTMD_PATH", w.stderr)
	}
}

// TestOlderDaemon checks that an older daemon is warned of and used, never
// restarted.
func TestOlderDaemon(t *testing.T) {
	old := newDaemon("0.9.0", 7)
	w := newWorld(old, newDaemon("1.0.0", 8))
	if code := w.run(t, "next"); code != exitOK {
		t.Fatalf("exit %d; stderr %q", code, w.stderr)
	}
	if !strings.Contains(w.stderr.String(), "0.9.0") || !strings.Contains(w.stderr.String(), "ytm daemon restart") {
		t.Errorf("stderr %q: no warning", w.stderr)
	}
	if got := old.methods(); !slices.Equal(got, []api.Method{api.MethodNext}) {
		t.Errorf("calls %v, want only Next", got)
	}
	if w.dialer.spawned() != 0 {
		t.Error("started a daemon")
	}
}

// TestIncompatibleDaemon checks that a daemon of another protocol gets
// only the bootstrap calls.
func TestIncompatibleDaemon(t *testing.T) {
	newer := func() *daemon {
		d := newDaemon("2.0.0", 7)
		d.hello.Proto = api.Proto + 1
		return d
	}
	t.Run("next", func(t *testing.T) {
		d := newer()
		w := newWorld(d)
		if code := w.run(t, "next"); code != exitFailed {
			t.Errorf("exit %d, want %d", code, exitFailed)
		}
		if got := d.methods(); len(got) > 0 {
			t.Errorf("calls %v", got)
		}
		if !strings.Contains(w.stderr.String(), "Update YT Music Player") {
			t.Errorf("stderr %q", w.stderr)
		}
	})
	t.Run("stop", func(t *testing.T) {
		d := newer()
		w := newWorld(d)
		if code := w.run(t, "daemon", "stop"); code != exitOK {
			t.Errorf("exit %d; stderr %q", code, w.stderr)
		}
		if got := d.methods(); !slices.Equal(got, []api.Method{api.MethodShutdown}) {
			t.Errorf("calls %v, want Shutdown", got)
		}
	})
	t.Run("version", func(t *testing.T) {
		w := newWorld(newer())
		if code := w.run(t, "version"); code != exitOK {
			t.Errorf("exit %d", code)
		}
		want := "ytm 1.0.0 (proto 2)\nytmd 2.0.0 (proto 3)\n"
		if got := w.stdout.String(); got != want {
			t.Errorf("stdout %q, want %q", got, want)
		}
	})
}

func TestVersion(t *testing.T) {
	w := newWorld(nil)
	if code := w.run(t, "version"); code != exitOK {
		t.Errorf("exit %d", code)
	}
	if got, want := w.stdout.String(), "ytm 1.0.0 (proto 2)\n"; got != want {
		t.Errorf("stdout %q, want %q", got, want)
	}
}

func TestDaemonStop(t *testing.T) {
	d := newDaemon("1.0.0", 7)
	w := newWorld(d)
	if code := w.run(t, "daemon", "stop"); code != exitOK {
		t.Fatalf("exit %d; stderr %q", code, w.stderr)
	}
	if !d.gone() {
		t.Error("the daemon runs on")
	}
	if code := w.run(t, "daemon", "status"); code != exitStopped {
		t.Errorf("status after stop: exit %d, want %d", code, exitStopped)
	}
}

// TestDaemonRestart checks the hand-off: the old daemon gets Handoff, and
// ytm starts its own and waits until it greets.
func TestDaemonRestart(t *testing.T) {
	old, own := newDaemon("0.9.0", 7), newDaemon("1.0.0", 8)
	w := newWorld(old, own)
	if code := w.run(t, "daemon", "restart"); code != exitOK {
		t.Fatalf("exit %d; stderr %q", code, w.stderr)
	}
	call := old.last(t)
	if call.Method != api.MethodHandoff || call.Hand == nil || call.Hand.Wait <= 0 {
		t.Errorf("old daemon got %+v, want a Handoff with a wait", call)
	}
	if w.dialer.spawned() != 1 {
		t.Errorf("%d spawns, want 1", w.dialer.spawned())
	}
	if !strings.Contains(w.stdout.String(), "process 8") {
		t.Errorf("stdout %q does not tell of the new daemon", w.stdout)
	}
	if strings.Contains(w.stderr.String(), "older") {
		t.Errorf("warned of the old daemon: %q", w.stderr)
	}
}

func TestDaemonStatus(t *testing.T) {
	w := newWorld(newDaemon("1.0.0", 7))
	if code := w.run(t, "daemon", "status"); code != exitOK {
		t.Errorf("exit %d", code)
	}
	if got, want := w.stdout.String(), "The background player 1.0.0 is running (process 7).\n"; got != want {
		t.Errorf("stdout %q, want %q", got, want)
	}
}

func TestLogs(t *testing.T) {
	w := newWorld(nil)
	if code := w.run(t, "logs"); code != exitOK {
		t.Errorf("exit %d", code)
	}
	want := w.env.paths.Log("ytmd") + "\n" + w.env.paths.Log("ytm") + "\n"
	if got := w.stdout.String(); got != want {
		t.Errorf("stdout %q, want %q", got, want)
	}
}

// TestFailureLogged checks that a failure reaches the log at debug level.
func TestFailureLogged(t *testing.T) {
	logged := logAtDebug(t)
	w := newWorld(nil)
	w.run(t, "next")
	if !strings.Contains(logged(), "ytm: command failed") {
		t.Errorf("log %q", logged())
	}
}
