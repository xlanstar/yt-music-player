package main

import (
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"yt-music-player/internal/settings"

	"github.com/egoist/mygo/ui"
)

// fakeStartup stands in for package autostart: it records the calls, off
// the UI thread, and fails those the test makes fail.
type fakeStartup struct {
	mu      sync.Mutex
	calls   []string
	enabled bool
	err     error // what enable, disable, enablePath and disablePath return
}

// Where the fake finds the app's helpers.
var (
	fakeDaemon = filepath.FromSlash("/app/ytmd")
	fakeYtmDir = filepath.FromSlash("/app")
)

func (f *fakeStartup) note(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeStartup) took() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := f.calls
	f.calls = nil
	return calls
}

// count returns how many calls were made since took.
func (f *fakeStartup) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeStartup) failure() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

func (f *fakeStartup) system() startupSystem {
	return startupSystem{
		enable:  func(ytmd string) error { f.note("enable " + ytmd); return f.failure() },
		disable: func() error { f.note("disable"); return f.failure() },
		status: func() (bool, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.enabled, nil
		},
		repair:      func(ytmd string) error { f.note("repair " + ytmd); return nil },
		enablePath:  func(dir string) error { f.note("enablePath " + dir); return f.failure() },
		disablePath: func(dir string) error { f.note("disablePath " + dir); return f.failure() },
		repairPath:  func(old, dir string) error { f.note("repairPath " + old + " " + dir); return nil },
		daemon:      func() (string, error) { return fakeDaemon, nil },
		ytmDir:      func() (string, error) { return fakeYtmDir, nil },
	}
}

// startupApp is a lookApp whose settings hold terminalPath, started over
// f as a release build, or a development one.
func startupApp(t *testing.T, f *fakeStartup, terminalPath string, dev bool) (*app, posts, settings.Dir, *ui.Tester) {
	t.Helper()
	a, q, dir := lookApp(t, func(dir settings.Dir) {
		if err := dir.Save(settings.Settings{TerminalPath: terminalPath}); err != nil {
			t.Fatal(err)
		}
	})
	a.startup.hasTerminal = true
	a.startStartup(f.system(), dev)
	tt := newTester(a)
	eventually(t, tt, q, "read the login item", func() bool { return a.startup.isKnown })
	return a, q, dir, tt
}

func absDaemon(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(fakeDaemon)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// At start, what is on points at this app's helpers, as after the app
// moved; a development build leaves them alone.
func TestStartupRepairs(t *testing.T) {
	old := filepath.FromSlash("/old")
	f := &fakeStartup{enabled: true}
	a, _, dir, _ := startupApp(t, f, old, false)
	want := []string{"repair " + absDaemon(t), "repairPath " + old + " " + fakeYtmDir}
	if got := f.took(); !slices.Equal(got, want) {
		t.Errorf("calls %q, want %q", got, want)
	}
	if !a.startup.atLogin || !a.startup.inTerminal || saved(t, a, dir).TerminalPath != fakeYtmDir {
		t.Errorf("at login %v, in terminal %v, kept %q", a.startup.atLogin, a.startup.inTerminal, a.look.set.TerminalPath)
	}

	f = &fakeStartup{enabled: true}
	a, _, _, _ = startupApp(t, f, old, true)
	if got := f.took(); len(got) != 0 || !a.startup.atLogin {
		t.Errorf("a development build called %q", got)
	}
}

func TestStartAtLoginSwitch(t *testing.T) {
	f := &fakeStartup{}
	a, q, _, tt := startupApp(t, f, "", false)
	openLookSheet(t, tt, q, a)
	if !tt.HasText("Start background player at login") || a.startup.atLogin {
		t.Fatalf("no switch, or on, texts %q", tt.Texts())
	}
	click(t, tt, "Start background player at login")
	eventually(t, tt, q, "turned on", func() bool { return f.count() > 0 && !a.startup.login.IsRunning() })
	if got := f.took(); !slices.Equal(got, []string{"enable " + absDaemon(t)}) {
		t.Errorf("turning it on called %q, on %v", got, a.startup.atLogin)
	}

	// A failure turns the switch back, and says why.
	f.err = errors.New("read-only")
	click(t, tt, "Start background player at login")
	eventually(t, tt, q, "failed", func() bool { return f.count() > 0 && !a.startup.login.IsRunning() })
	frames(tt, 2)
	if got := f.took(); !slices.Equal(got, []string{"disable"}) || !toasted(tt, "Couldn’t change starting at login") {
		t.Errorf("calls %q, announced %q", got, tt.Announcements())
	}
}

func TestTerminalSwitch(t *testing.T) {
	f := &fakeStartup{}
	a, q, dir, tt := startupApp(t, f, "", false)
	openLookSheet(t, tt, q, a)
	click(t, tt, "Use ytm in a terminal")
	eventually(t, tt, q, "turned on", func() bool { return saved(t, a, dir).TerminalPath == fakeYtmDir })
	click(t, tt, "Use ytm in a terminal")
	eventually(t, tt, q, "turned off", func() bool { return saved(t, a, dir).TerminalPath == "" })
	want := []string{"enablePath " + fakeYtmDir, "disablePath " + fakeYtmDir}
	if got := f.took(); !slices.Equal(got, want) || a.startup.inTerminal {
		t.Errorf("calls %q, want %q", got, want)
	}

	f.err = errors.New("access denied")
	click(t, tt, "Use ytm in a terminal")
	eventually(t, tt, q, "failed", func() bool { return toasted(tt, "Couldn’t change ytm in the terminal") })
	if a.startup.inTerminal || saved(t, a, dir).TerminalPath != "" {
		t.Error("a failure left the switch on")
	}

	// Only Windows offers it.
	a.startup.hasTerminal = false
	frames(tt, 2)
	if tt.HasText("Use ytm in a terminal") {
		t.Error("offered without a Path to put it on")
	}
}
