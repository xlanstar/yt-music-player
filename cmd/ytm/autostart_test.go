package main

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"yt-music-player/internal/version"
)

func TestAutostart(t *testing.T) {
	w := newWorld(nil)
	steps := []struct {
		arg, want string
		enabled   bool
	}{
		{"status", "doesn’t start", false},
		{"on", "starts when", true},
		{"status", "starts when", true},
		{"off", "doesn’t start", false},
	}
	for _, s := range steps {
		w.stdout.Reset()
		if code := w.run(t, "autostart", s.arg); code != exitOK {
			t.Fatalf("autostart %s: exit %d; stderr %q", s.arg, code, w.stderr)
		}
		if !strings.Contains(w.stdout.String(), s.want) {
			t.Errorf("autostart %s: stdout %q", s.arg, w.stdout)
		}
		if enabled, ytmd, _ := w.env.autostart.status(); enabled != s.enabled || (enabled && ytmd != daemonPath) {
			t.Errorf("autostart %s: enabled %v (%q)", s.arg, enabled, ytmd)
		}
	}
	if got := w.autostart.repaired(); len(got) > 0 {
		t.Errorf("autostart repaired %v while switched", got)
	}
}

func TestAutostartNotFound(t *testing.T) {
	w := newWorld(nil)
	w.env.findDaemon = func() (string, error) { return "", errors.New("ytmd not found") }
	if code := w.run(t, "autostart", "on"); code != exitFailed {
		t.Errorf("exit %d, want %d", code, exitFailed)
	}
}

// TestRepair checks that a command repairs the login entry, unless a
// development build runs it, and never fails for it.
func TestRepair(t *testing.T) {
	w := newWorld(newDaemon("1.0.0", 7))
	w.autostart.repairErr = errors.New("read-only")
	if code := w.run(t, "next"); code != exitOK {
		t.Errorf("exit %d", code)
	}
	if got := w.autostart.repaired(); !slices.Equal(got, []string{daemonPath}) {
		t.Errorf("repaired %v, want %q", got, daemonPath)
	}

	w = newWorld(newDaemon("1.0.0", 7))
	w.env.version = version.Dev
	w.run(t, "next")
	if got := w.autostart.repaired(); len(got) > 0 {
		t.Errorf("a development build repaired %v", got)
	}
}
