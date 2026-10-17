package ipc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"yt-music-player/internal/api"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o700); err != nil {
		t.Fatal(err)
	}
}

// The lookup goes $YTMD_PATH, then beside the program, then inside the
// bundle beside it (ytm in Contents/Resources), then the bundle in
// Resources (Desktop in Contents/MacOS).
func TestFindDaemonOrder(t *testing.T) {
	t.Setenv("YTMD_PATH", "")
	app := filepath.Join(t.TempDir(), "YT Music Player.app", "Contents")
	desktop := filepath.Join(app, "MacOS", "yt-music-player")
	ytm := filepath.Join(app, "Resources", "ytm")
	bundled := filepath.Join(app, "Resources", "YT Music Player Helper.app", "Contents", "MacOS", "ytmd")
	touch(t, bundled)

	for _, exe := range []string{desktop, ytm} {
		if got, err := findDaemon(exe, "darwin"); err != nil || filepath.Clean(got) != bundled {
			t.Errorf("from %s: %q, %v; want %q", exe, got, err, bundled)
		}
	}
	// Linux and Windows have no bundle.
	if _, err := findDaemon(ytm, "linux"); !errors.Is(err, api.ErrDaemonNotFound) {
		t.Errorf("linux found a bundle: %v", err)
	}

	beside := filepath.Join(app, "Resources", "ytmd")
	touch(t, beside)
	if got, err := findDaemon(ytm, "darwin"); err != nil || got != beside {
		t.Errorf("beside: %q, %v; want %q", got, err, beside)
	}
	if got, err := findDaemon(ytm, "linux"); err != nil || got != beside {
		t.Errorf("linux beside: %q, %v; want %q", got, err, beside)
	}
	exe := filepath.Join(app, "Resources", "ytmd.exe")
	touch(t, exe)
	if got, err := findDaemon(ytm, "windows"); err != nil || got != exe {
		t.Errorf("windows: %q, %v; want %q", got, err, exe)
	}

	override := filepath.Join(t.TempDir(), "build", "ytmd")
	touch(t, override)
	t.Setenv("YTMD_PATH", override)
	if got, err := findDaemon(ytm, "darwin"); err != nil || got != override {
		t.Errorf("YTMD_PATH: %q, %v; want %q", got, err, override)
	}
}

func TestFindDaemonMissing(t *testing.T) {
	t.Setenv("YTMD_PATH", "")
	_, err := findDaemon(filepath.Join(t.TempDir(), "ytm"), "darwin")
	if !errors.Is(err, api.ErrDaemonNotFound) || !strings.Contains(err.Error(), "YTMD_PATH") {
		t.Errorf("err %v, want ErrDaemonNotFound naming YTMD_PATH", err)
	}
	t.Setenv("YTMD_PATH", filepath.Join(t.TempDir(), "nothing"))
	if _, err := findDaemon("/x/ytm", "linux"); !errors.Is(err, api.ErrDaemonNotFound) {
		t.Errorf("YTMD_PATH to nothing: err %v", err)
	}
}

func TestDialOrSpawnDialsFirst(t *testing.T) {
	_, path := listen(t)
	d := Dialer{Socket: path, Spawn: func() error { t.Error("spawned with a daemon listening"); return nil }}
	c, err := d.DialOrSpawn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
}

func TestDialOrSpawnWaits(t *testing.T) {
	path := filepath.Join(shortDir(t), "s.sock")
	spawned := 0
	d := Dialer{Socket: path, every: time.Millisecond, Spawn: func() error {
		spawned++
		go func() {
			time.Sleep(20 * time.Millisecond)
			l, err := Listen(path)
			if err != nil {
				t.Error(err)
				return
			}
			t.Cleanup(func() { _ = l.Close() })
		}()
		return nil
	}}
	c, err := d.DialOrSpawn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	if spawned != 1 {
		t.Errorf("spawned %d times, want 1", spawned)
	}
}

func TestDialOrSpawnFails(t *testing.T) {
	path := filepath.Join(shortDir(t), "s.sock")
	d := Dialer{Socket: path, Spawn: func() error { return api.ErrDaemonNotFound }}
	if _, err := d.DialOrSpawn(t.Context()); !errors.Is(err, api.ErrDaemonNotFound) {
		t.Errorf("not found: err %v", err)
	}
	// Started, but never listening.
	d = Dialer{Socket: path, every: time.Millisecond, timeout: 20 * time.Millisecond, Spawn: func() error { return nil }}
	if _, err := d.DialOrSpawn(t.Context()); !errors.Is(err, api.ErrDaemonUnavailable) {
		t.Errorf("never listening: err %v, want ErrDaemonUnavailable", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := d.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled: err %v", err)
	}
}

// The test binary, as a daemon, starts detached and takes the connection.
func TestDialOrSpawnStarts(t *testing.T) {
	path := filepath.Join(shortDir(t), "d.sock")
	t.Setenv(fakeDaemonEnv, path)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	d := Dialer{Socket: path, Spawn: func() error { return Spawn(exe) }}
	c, err := d.DialOrSpawn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
}
