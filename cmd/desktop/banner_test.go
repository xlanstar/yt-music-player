package main

import (
	"slices"
	"testing"
	"yt-music-player/internal/api"
	"yt-music-player/internal/remote"
)

// The banner says how the connection to ytmd is when it is not as it
// should be, with what may mend it, and says nothing once it mends.
func TestBanner(t *testing.T) {
	older := remote.Peer{Hello: api.Hello{Version: "0.3.0"}, Version: "0.4.0"}
	newer := remote.Peer{Hello: api.Hello{Version: "0.5.0"}, Version: "0.4.0"}
	for _, tc := range []struct {
		name    string
		link    remote.Link
		text    string
		actions []string
	}{
		{"Reconnecting", remote.Link{Health: remote.Reconnecting}, "Reconnecting to the background player…", nil},
		{"Unresponsive", remote.Link{Health: remote.Unresponsive}, "The background player isn’t responding.", []string{"Restart", "View Log"}},
		{"Unavailable", remote.Link{Health: remote.Unavailable}, "Can’t reach the background player. Still trying…", []string{"Retry", "View Log"}},
		{
			"DaemonOutdated",
			remote.Link{Health: remote.DaemonOutdated, Daemon: older},
			"The background player (0.3.0) is older than this app (0.4.0). Update YT Music Player.",
			[]string{"Retry", "View Log"},
		},
		{
			"FrontendOutdated",
			remote.Link{Health: remote.FrontendOutdated, Daemon: newer},
			"The background player (0.5.0) is newer than this app (0.4.0). Update YT Music Player.",
			[]string{"Retry", "View Log"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := idleApp(t)
			tt := newTester(a)
			connected := a.daemon.link
			a.daemon.link = tc.link
			tt.Frame()
			wantTexts(t, tt, tc.text)
			for _, action := range []string{"Restart", "Retry", "View Log"} {
				if tt.HasText(action) != slices.Contains(tc.actions, action) {
					t.Errorf("%s offered %v, want %q", action, tt.HasText(action), tc.actions)
				}
			}
			if !slices.Contains(tt.Announcements(), tc.text) {
				t.Errorf("not announced, announced %q", tt.Announcements())
			}
			if slices.Contains(tc.actions, "View Log") {
				click(t, tt, "View Log")
				if got := tt.OpenedURLs(); !slices.Equal(got, []string{fileURL(testLog)}) {
					t.Errorf("opened %q, want the log", got)
				}
			}
			a.daemon.link = connected
			tt.Frame()
			if tt.HasText(tc.text) {
				t.Error("the banner stayed once connected")
			}
		})
	}
}

// Retry connects at once; connected, there is nothing to do.
func TestBannerRetry(t *testing.T) {
	a := idleApp(t)
	tt := newTester(a)
	a.daemon.link.Health = remote.Unavailable
	tt.Frame()
	click(t, tt, "Retry")
	settled(t, a)
	tt.Frame()
	if a.daemon.link.Health != remote.Unavailable || !tt.HasText("Retry") {
		t.Errorf("health %v", a.daemon.link.Health)
	}
}

func TestFileURL(t *testing.T) {
	for path, want := range map[string]string{
		"/Users/ada/Library/Caches/yt-music-player/logs/ytmd.log": "file:///Users/ada/Library/Caches/yt-music-player/logs/ytmd.log",
		"/home/ada/.cache/yt music/ytmd.log":                      "file:///home/ada/.cache/yt%20music/ytmd.log",
	} {
		if got := fileURL(path); got != want {
			t.Errorf("fileURL(%q) = %q, want %q", path, got, want)
		}
	}
}

// Quit and Stop Playback stops ytmd before quitting, which leaves no
// Supervisor to start it again.
func TestQuitAndStop(t *testing.T) {
	a, q := testApp(t)
	tt := newTester(a)
	hasQuit := false
	a.daemon.quit = func() { hasQuit = true }
	a.quitAndStop()
	eventually(t, tt, q, "quit", func() bool { return hasQuit })
	select {
	case <-q.ytmd.svc.Done():
	default:
		t.Error("ytmd still runs")
	}
}
