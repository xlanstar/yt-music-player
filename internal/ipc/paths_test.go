package ipc

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestSuffix(t *testing.T) {
	for _, tc := range []struct {
		placeholder, dev bool
		want             string
	}{
		{false, false, ""},
		{false, true, "-dev"},
		{true, false, "-placeholder"},
		{true, true, "-placeholder-dev"},
	} {
		if got := suffix(tc.placeholder, tc.dev); got != tc.want {
			t.Errorf("suffix(%v, %v) = %q, want %q", tc.placeholder, tc.dev, got, tc.want)
		}
	}
}

func TestPathsCarrySuffix(t *testing.T) {
	r := Roots{Config: "/c", Cache: "/k", Runtime: "/run/user/1", Temp: "/t", UID: 1}
	p, err := resolve(r, "linux", "-placeholder-dev")
	if err != nil {
		t.Fatal(err)
	}
	want := Paths{
		Socket: "/run/user/1/yt-music-player/ytmd-placeholder-dev.sock",
		Lock:   "/run/user/1/yt-music-player/ytmd-placeholder-dev.lock",
		State:  "/c/yt-music-player/daemon-placeholder-dev.json",
		Logs:   "/k/yt-music-player/logs",
	}
	if p.Socket != fp(want.Socket) || p.Lock != fp(want.Lock) || p.State != fp(want.State) || p.Logs != fp(want.Logs) {
		t.Errorf("got %+v, want %+v", p, want)
	}
	if got := p.Log("ytmd"); got != fp("/k/yt-music-player/logs/ytmd-placeholder-dev.log") {
		t.Errorf("Log = %q", got)
	}

	// Resolve adds this build's suffix to dev's.
	p, err = Resolve(r, true)
	if err != nil {
		t.Fatal(err)
	}
	if want := suffix(isPlaceholder, true) + ".sock"; !strings.HasSuffix(p.Socket, "ytmd"+want) {
		t.Errorf("Socket = %q, want ytmd%s", p.Socket, want)
	}
}

// fp makes a slash path the OS's, as filepath.Join does.
func fp(path string) string { return filepath.FromSlash(path) }

func TestSocketPaths(t *testing.T) {
	long := "/Users/" + strings.Repeat("a", 80)
	for _, tc := range []struct {
		name     string
		goos     string
		roots    Roots
		want     string
		isShared bool
	}{
		{
			"darwin", "darwin",
			Roots{Config: "/Users/u/Library/Application Support", Temp: "/var/folders/x/T", UID: 501},
			"/Users/u/Library/Application Support/yt-music-player/ytmd.sock", false,
		},
		{
			"darwin long", "darwin",
			Roots{Config: long, Temp: "/var/folders/x/T", UID: 501},
			"/var/folders/x/T/yt-music-player-501/ytmd.sock", true,
		},
		{
			"linux runtime", "linux",
			Roots{Config: "/home/u/.config", Runtime: "/run/user/1000", Temp: "/tmp", UID: 1000},
			"/run/user/1000/yt-music-player/ytmd.sock", false,
		},
		{
			"linux tmp", "linux",
			Roots{Config: "/home/u/.config", Temp: "/tmp", UID: 1000},
			"/tmp/yt-music-player-1000/ytmd.sock", true,
		},
		{
			"linux long runtime", "linux",
			Roots{Runtime: long + long, Temp: "/tmp", UID: 1000},
			"/tmp/yt-music-player-1000/ytmd.sock", true,
		},
		{
			"windows", "windows",
			Roots{Cache: "/C/Users/u/AppData/Local", Temp: "/C/Users/u/AppData/Local/Temp", UID: -1},
			"/C/Users/u/AppData/Local/yt-music-player/ytmd.sock", false,
		},
		{
			"windows long", "windows",
			Roots{Cache: long + long, Temp: "/C/Temp", UID: -1},
			"/C/Temp/yt-music-player/ytmd.sock", false,
		},
	} {
		p, err := resolve(tc.roots, tc.goos, "")
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if p.Socket != fp(tc.want) || p.isShared != tc.isShared {
			t.Errorf("%s: socket %q (shared %v), want %q (%v)", tc.name, p.Socket, p.isShared, tc.want, tc.isShared)
		}
		if p.Lock != filepath.Join(filepath.Dir(p.Socket), "ytmd.lock") {
			t.Errorf("%s: lock %q is not next to the socket", tc.name, p.Lock)
		}
	}
}

// macOS's sun_path takes 103 bytes, the others' 107.
func TestSocketPathLimit(t *testing.T) {
	for goos, limit := range map[string]int{"darwin": 103, "linux": 107, "windows": 107} {
		const name = "/yt-music-player/ytmd.sock"
		fits := "/" + strings.Repeat("a", limit-len(name)-1)
		roots := Roots{Config: fits, Cache: fits, Runtime: fits, Temp: "/t", UID: 1}
		p, err := resolve(roots, goos, "")
		if err != nil || len(p.Socket) != limit || p.isShared {
			t.Errorf("%s: %d bytes: socket %q, err %v", goos, limit, p.Socket, err)
		}
		over := fits + "a"
		roots = Roots{Config: over, Cache: over, Runtime: over, Temp: "/t", UID: 1}
		if p, err := resolve(roots, goos, ""); err != nil || !strings.HasPrefix(p.Socket, fp("/t/")) {
			t.Errorf("%s: %d bytes: socket %q, err %v; want it in /t", goos, limit+1, p.Socket, err)
		}
		roots.Temp = over
		if _, err := resolve(roots, goos, ""); !errors.Is(err, ErrPathTooLong) {
			t.Errorf("%s: no short path: err %v, want ErrPathTooLong", goos, err)
		}
	}
}
