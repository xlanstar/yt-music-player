package main

import (
	"math"
	"slices"
	"testing"
	"yt-music-player/internal/api"
)

func TestParseAmount(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want amount
		ok   bool
	}{
		{"30", amount{30, false}, true},
		{"1.5", amount{1.5, false}, true},
		{"+10", amount{10, true}, true},
		{"-10", amount{-10, true}, true},
		{"-0", amount{0, true}, true},
		{"", amount{}, false},
		{"+", amount{}, false},
		{"ten", amount{}, false},
		{"NaN", amount{}, false},
		{"-Inf", amount{}, false},
		{"1:30", amount{}, false},
	} {
		got, ok := parseAmount(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("parseAmount(%q) = %+v, %v; want %+v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestSeekTo(t *testing.T) {
	s := &api.Status{State: api.State{Position: 20, Duration: 213}}
	unknown := &api.Status{State: api.State{Position: 20}}
	for _, tc := range []struct {
		a      amount
		status *api.Status
		want   float64
	}{
		{amount{90, false}, s, 90},
		{amount{0, false}, s, 0},
		{amount{10, true}, s, 30},
		{amount{-5, true}, s, 15},
		{amount{-30, true}, s, 0},
		{amount{500, true}, s, 213},
		{amount{500, true}, unknown, 520},
	} {
		if got := seekTo(tc.a, tc.status); got != tc.want {
			t.Errorf("seekTo(%+v) from %v = %v, want %v", tc.a, tc.status.State.Position, got, tc.want)
		}
	}
}

func TestVolumeTo(t *testing.T) {
	s := &api.Status{State: api.State{Volume: 0.7}}
	for _, tc := range []struct {
		a    amount
		want float64
	}{
		{amount{50, false}, 0.5},
		{amount{0, false}, 0},
		{amount{100, false}, 1},
		{amount{10, true}, 0.8},
		{amount{-25, true}, 0.45},
		{amount{-100, true}, 0},
		{amount{50, true}, 1},
	} {
		if got := volumeTo(tc.a, s); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("volumeTo(%+v) from 70 = %v, want %v", tc.a, got, tc.want)
		}
	}
}

// TestRelative checks that relative seeks and volumes read the status
// first, and absolute ones do not.
func TestRelative(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		calls []api.Method
		check func(api.Call) bool
	}{
		{[]string{"seek", "+30"}, []api.Method{api.MethodStatus, api.MethodSeek}, func(c api.Call) bool { return c.Seek.Seconds == 111.5 }},
		{[]string{"seek", "-100"}, []api.Method{api.MethodStatus, api.MethodSeek}, func(c api.Call) bool { return c.Seek.Seconds == 0 }},
		{[]string{"seek", "12"}, []api.Method{api.MethodSeek}, func(c api.Call) bool { return c.Seek.Seconds == 12 }},
		{[]string{"volume", "-20"}, []api.Method{api.MethodStatus, api.MethodSetVolume}, func(c api.Call) bool { return math.Abs(c.Volume.Volume-0.5) < 1e-9 }},
		{[]string{"volume", "35"}, []api.Method{api.MethodSetVolume}, func(c api.Call) bool { return c.Volume.Volume == 0.35 }},
	} {
		d := newDaemon("1.0.0", 7)
		d.status = playingStatus()
		w := newWorld(d)
		if code := w.run(t, tc.args...); code != exitOK {
			t.Errorf("%v: exit %d; stderr %q", tc.args, code, w.stderr)
			continue
		}
		if got := d.methods(); !slices.Equal(got, tc.calls) {
			t.Errorf("%v: calls %v, want %v", tc.args, got, tc.calls)
		}
		if c := d.last(t); !tc.check(c) {
			t.Errorf("%v: sent %+v %+v", tc.args, c.Seek, c.Volume)
		}
	}
}

// TestControlFailed checks a control the daemon refused.
func TestControlFailed(t *testing.T) {
	d := newDaemon("1.0.0", 7)
	d.answer = func(*api.Call) *api.Reply { return &api.Reply{Err: &api.Error{Code: api.CodeInternal}} }
	w := newWorld(d)
	if code := w.run(t, "next"); code != exitFailed {
		t.Errorf("exit %d, want %d", code, exitFailed)
	}
	want := "Couldn’t change playback: the background player ran into a problem; its log is at " + w.env.paths.Log("ytmd") + "\n"
	if got := w.stderr.String(); got != want {
		t.Errorf("stderr %q, want %q", got, want)
	}
}
