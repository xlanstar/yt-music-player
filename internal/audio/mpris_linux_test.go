package audio

import (
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// TestMPRIS runs where there is a session bus, as on a desktop or under
// dbus-run-session; CI's runners have none.
func TestMPRIS(t *testing.T) {
	client, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Skip("no session bus:", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	commands := make(chan Event, 8)
	c := newControls(func(ev Event) { commands <- ev })
	m, ok := c.(*mpris)
	if !ok {
		t.Fatalf("controls %T, want MPRIS", c)
	}
	t.Cleanup(c.close)
	c.publish(nowPlaying{
		Title: "Tone", Artist: "ffmpeg", Album: "Tests",
		duration: 6, elapsed: 2, state: statePlaying, advancing: true,
	})
	obj := client.Object(mprisName, mprisPath)

	var props map[string]dbus.Variant
	if err := obj.Call(propsIface+".GetAll", 0, mprisPlayer).Store(&props); err != nil {
		t.Fatal(err)
	}
	if s := props["PlaybackStatus"].Value(); s != "Playing" {
		t.Errorf("PlaybackStatus %v", s)
	}
	md, _ := props["Metadata"].Value().(map[string]dbus.Variant)
	if md["xesam:title"].Value() != "Tone" || md["mpris:length"].Value() != int64(6e6) {
		t.Errorf("Metadata %v", md)
	}
	if pos, _ := props["Position"].Value().(int64); pos < 2e6 || pos > 3e6 {
		t.Errorf("Position %v µs, want about 2 s", pos)
	}
	var identity dbus.Variant
	if err := obj.Call(propsIface+".Get", 0, mprisRoot, "Identity").Store(&identity); err != nil || identity.Value() != "YT Music Player" {
		t.Errorf("Identity %v, %v", identity, err)
	}

	m.mu.Lock()
	track := m.trackIDLocked()
	m.mu.Unlock()
	for _, tc := range []struct {
		method string
		args   []any
		want   Event
	}{
		{"PlayPause", nil, Event{Kind: CommandToggle}},
		{"Next", nil, Event{Kind: CommandNext}},
		{"Previous", nil, Event{Kind: CommandPrev}},
		{"SetPosition", []any{track, int64(4e6)}, Event{Kind: CommandSeek, At: 4}},
	} {
		if err := obj.Call(mprisPlayer+"."+tc.method, 0, tc.args...).Err; err != nil {
			t.Fatalf("%s: %v", tc.method, err)
		}
		select {
		case ev := <-commands:
			if ev != tc.want {
				t.Errorf("%s sent %+v, want %+v", tc.method, ev, tc.want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s sent nothing", tc.method)
		}
	}
	// Seek is relative: about 2 s in, 3 s on.
	if err := obj.Call(mprisPlayer+".Seek", 0, int64(3e6)).Err; err != nil {
		t.Fatal(err)
	}
	if ev := <-commands; ev.Kind != CommandSeek || ev.At < 5 || ev.At > 6 {
		t.Errorf("Seek sent %+v", ev)
	}
	// Another track's id is stale: nothing.
	if err := obj.Call(mprisPlayer+".SetPosition", 0, dbus.ObjectPath("/old/track"), int64(1e6)).Err; err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-commands:
		t.Errorf("a stale track's SetPosition sent %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}
