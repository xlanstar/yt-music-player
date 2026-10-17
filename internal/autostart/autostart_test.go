package autostart

import (
	"bytes"
	"cmp"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

const appYtmd = "/Applications/YT Music Player.app/Contents/Resources/YT Music Player Helper.app/Contents/MacOS/ytmd"

// wantPlist is what Enable writes for appYtmd under the home @HOME@.
const wantPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>dev.lanstar.ytmusicplayer.daemon</string>
	<key>AssociatedBundleIdentifiers</key>
	<array>
		<string>dev.lanstar.ytmusicplayer</string>
	</array>
	<key>ProgramArguments</key>
	<array>
		<string>/bin/sh</string>
		<string>-c</string>
		<string>[ -x "$0" ] &amp;&amp; exec "$0"; rm -f '@HOME@/Library/LaunchAgents/dev.lanstar.ytmusicplayer.daemon.plist'; launchctl disable gui/$(id -u)/dev.lanstar.ytmusicplayer.daemon</string>
		<string>/Applications/YT Music Player.app/Contents/Resources/YT Music Player Helper.app/Contents/MacOS/ytmd</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>LimitLoadToSessionType</key>
	<string>Aqua</string>
	<key>ProcessType</key>
	<string>Interactive</string>
</dict>
</plist>
`

// oldPlist is what versions before the self-removing script wrote.
const oldPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>dev.lanstar.ytmusicplayer.daemon</string>
	<key>ProgramArguments</key>
	<array>
		<string>/Applications/YT Music Player.app/Contents/Resources/YT Music Player Helper.app/Contents/MacOS/ytmd</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>LimitLoadToSessionType</key>
	<string>Aqua</string>
	<key>ProcessType</key>
	<string>Interactive</string>
</dict>
</plist>
`

const wantUnit = `[Unit]
Description=YT Music Player background player
ConditionPathExists=/opt/yt-music-player/ytmd

[Service]
ExecStart=/opt/yt-music-player/ytmd
Restart=no

[Install]
WantedBy=default.target
`

// fileCase is a platform whose entry is a file, over a temporary home.
type fileCase struct {
	name    string
	make    func(home string, run runner) item
	file    string // under home
	ytmd    string
	want    string // the file Enable writes for ytmd, @HOME@ the home
	on, off []string
}

var fileCases = []fileCase{
	{
		name: "launch agent",
		make: func(home string, run runner) item { return newLaunchAgent(home, 501, run) },
		file: "Library/LaunchAgents/dev.lanstar.ytmusicplayer.daemon.plist",
		ytmd: appYtmd,
		want: wantPlist,
		on:   []string{"launchctl", "enable", "gui/501/dev.lanstar.ytmusicplayer.daemon"},
		off:  []string{"launchctl", "disable", "gui/501/dev.lanstar.ytmusicplayer.daemon"},
	},
	{
		name: "systemd unit",
		make: func(home string, run runner) item { return newSystemdUnit(filepath.Join(home, ".config"), run) },
		file: ".config/systemd/user/ytmd.service",
		ytmd: "/opt/yt-music-player/ytmd",
		want: wantUnit,
		on:   []string{"systemctl", "--user", "enable", "ytmd.service"},
		off:  []string{"systemctl", "--user", "disable", "ytmd.service"},
	},
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func wantStatus(t *testing.T, it item, enabled bool, ytmd string) {
	t.Helper()
	gotOn, gotPath, err := it.status()
	if err != nil || gotOn != enabled || gotPath != ytmd {
		t.Fatalf("status = %v, %q, %v; want %v, %q, nil", gotOn, gotPath, err, enabled, ytmd)
	}
}

func wantCommands(t *testing.T, c *commands, want ...[]string) {
	t.Helper()
	if !slices.EqualFunc(c.argv, want, slices.Equal) {
		t.Fatalf("ran %q, want %q", c.argv, want)
	}
	c.argv = nil
}

func TestFileEntries(t *testing.T) {
	for _, tc := range fileCases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			var c commands
			it := tc.make(home, c.run)
			path := filepath.Join(home, filepath.FromSlash(tc.file))
			wantStatus(t, it, false, "")

			if err := it.enable(tc.ytmd); err != nil {
				t.Fatal(err)
			}
			wantCommands(t, &c, tc.on)
			if got, want := readFile(t, path), strings.ReplaceAll(tc.want, "@HOME@", home); got != want {
				t.Errorf("Enable wrote\n%s\nwant\n%s", got, want)
			}
			if info, err := os.Stat(path); err == nil && runtime.GOOS != "windows" && info.Mode().Perm() != 0o644 {
				t.Errorf("entry has mode %v, want 0644", info.Mode().Perm())
			}
			wantStatus(t, it, true, tc.ytmd)

			// Repair leaves an entry that is current alone, and points one
			// at a moved app, without telling the service manager.
			if err := it.repair(tc.ytmd); err != nil {
				t.Fatal(err)
			}
			moved := "/Users/me/Apps/ytmd"
			if err := it.repair(moved); err != nil {
				t.Fatal(err)
			}
			wantCommands(t, &c)
			wantStatus(t, it, true, moved)

			if err := it.disable(); err != nil {
				t.Fatal(err)
			}
			wantCommands(t, &c, tc.off)
			if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("Disable left the entry: %v", err)
			}
			wantStatus(t, it, false, "")

			// Off stays off: neither call does anything.
			if err := it.disable(); err != nil {
				t.Fatal(err)
			}
			if err := it.repair(tc.ytmd); err != nil {
				t.Fatal(err)
			}
			wantCommands(t, &c)
			wantStatus(t, it, false, "")
		})
	}
}

func TestFileEntryFailures(t *testing.T) {
	for _, tc := range fileCases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			c := commands{err: errCommand}
			it := tc.make(home, c.run)

			// An entry the service manager refused reads as off.
			if err := it.enable(tc.ytmd); !errors.Is(err, errCommand) {
				t.Fatalf("enable = %v, want the command's error", err)
			}
			wantStatus(t, it, false, "")

			// Off removes the file even when the command fails: without it
			// nothing starts.
			c.err = nil
			if err := it.enable(tc.ytmd); err != nil {
				t.Fatal(err)
			}
			c.err = errCommand
			if err := it.disable(); !errors.Is(err, errCommand) {
				t.Fatalf("disable = %v, want the command's error", err)
			}
			wantStatus(t, it, false, "")
		})
	}
}

// TestSwitchingStopsNothing holds that switching decides the next login
// only, so turning it off does not stop the music.
func TestSwitchingStopsNothing(t *testing.T) {
	for _, tc := range fileCases {
		t.Run(tc.name, func(t *testing.T) {
			var c commands
			it := tc.make(t.TempDir(), c.run)
			for range 2 {
				if err := it.enable(tc.ytmd); err != nil {
					t.Fatal(err)
				}
				if err := it.repair("/elsewhere/ytmd"); err != nil {
					t.Fatal(err)
				}
				if err := it.disable(); err != nil {
					t.Fatal(err)
				}
			}
			for _, argv := range c.argv {
				if stopsDaemon(argv) {
					t.Errorf("ran %q", argv)
				}
			}
		})
	}
}

func TestUnitExecStart(t *testing.T) {
	for _, tc := range []struct{ ytmd, line, condition string }{
		{"/opt/yt-music-player/ytmd", "ExecStart=/opt/yt-music-player/ytmd", ""},
		{"/home/me/.local/yt-music-player.app/ytmd", "ExecStart=/home/me/.local/yt-music-player.app/ytmd", ""},
		{"/home/me/My Apps/ytmd", `ExecStart="/home/me/My Apps/ytmd"`, ""},
		{"/home/me/100%/$HOME/ytmd", "ExecStart=/home/me/100%%/$$HOME/ytmd", "/home/me/100%%/$HOME/ytmd"},
		{`/home/me/a\b "c" 'd'/ytmd`, `ExecStart="/home/me/a\\b \"c\" 'd'/ytmd"`, ""},
		{"/home/me/音樂/ytmd", "ExecStart=/home/me/音樂/ytmd", ""},
	} {
		data := renderUnit(tc.ytmd)
		if !bytes.Contains(data, []byte("\n"+tc.line+"\n")) {
			t.Errorf("unit for %q lacks %s:\n%s", tc.ytmd, tc.line, data)
		}
		// Taken as it is but for specifiers: no quotes or escapes.
		condition := cmp.Or(tc.condition, tc.ytmd)
		if line := "\nConditionPathExists=" + condition + "\n"; !bytes.Contains(data, []byte(line)) {
			t.Errorf("unit for %q lacks ConditionPathExists=%s:\n%s", tc.ytmd, condition, data)
		}
		if got := unitProgram(data); got != tc.ytmd {
			t.Errorf("unitProgram = %q, want %q", got, tc.ytmd)
		}
	}
}

func TestUnit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("units name Unix paths")
	}
	got, err := Unit("/opt/yt-music-player/ytmd")
	if err != nil || string(got) != wantUnit {
		t.Errorf("Unit = %q, %v; want %q", got, err, wantUnit)
	}
	// The template Linux packages ship (scripts/build-helpers.sh), for the
	// Debian package's ytmd.
	if shipped := readFile(t, "../../packaging/linux/ytmd.service"); shipped != string(got) {
		t.Errorf("packaging/linux/ytmd.service = %q; want Unit's %q", shipped, got)
	}
	for _, bad := range []string{"", "ytmd", "./ytmd", "/opt/a\nb/ytmd", "/opt/\xff/ytmd"} {
		if _, err := Unit(bad); err == nil {
			t.Errorf("Unit(%q) succeeded", bad)
		}
	}
}

// TestRefusesBadPaths checks before touching the system.
func TestRefusesBadPaths(t *testing.T) {
	for _, bad := range []string{"", "ytmd", "bin/ytmd", "/opt/a\nb/ytmd"} {
		if Enable(bad) == nil {
			t.Errorf("Enable(%q) succeeded", bad)
		}
		if Repair(bad) == nil {
			t.Errorf("Repair(%q) succeeded", bad)
		}
	}
}

func TestRunKey(t *testing.T) {
	h := newFakeHive()
	it := runKey{reg: h}
	ytmd := `C:\Users\me\AppData\Local\Programs\YT Music Player\ytmd.exe`
	wantStatus(t, it, false, "")

	if err := it.enable(ytmd); err != nil {
		t.Fatal(err)
	}
	if got, want := h.values[runKeyPath+`\`+RunValue], (fakeValue{data: `"` + ytmd + `"`, kind: kindString}); got != want {
		t.Errorf("Run value = %+v, want %+v", got, want)
	}
	wantStatus(t, it, true, ytmd)

	writes := h.writes
	if err := it.repair(ytmd); err != nil {
		t.Fatal(err)
	}
	if h.writes != writes {
		t.Error("Repair rewrote a current value")
	}
	moved := `D:\Apps\YT Music Player\ytmd.exe`
	if err := it.repair(moved); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, it, true, moved)

	if err := it.disable(); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, it, false, "")
	if err := it.disable(); err != nil {
		t.Errorf("Disable when off = %v", err)
	}
	if err := it.repair(ytmd); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, it, false, "")
}

func TestRunKeyUnquoted(t *testing.T) {
	h := newFakeHive()
	h.values[runKeyPath+`\`+RunValue] = fakeValue{data: `C:\ytmd.exe`, kind: kindString}
	wantStatus(t, runKey{reg: h}, true, `C:\ytmd.exe`)
}

func TestPathOnlyOnWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("would change the user's Path")
	}
	dir := "/opt/yt-music-player"
	for name, err := range map[string]error{
		"EnablePath":  EnablePath(dir),
		"DisablePath": DisablePath(dir),
		"RepairPath":  RepairPath(dir, dir),
	} {
		if !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("%s = %v, want errors.ErrUnsupported", name, err)
		}
	}
	if _, err := PathEnabled(dir); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("PathEnabled = %v, want errors.ErrUnsupported", err)
	}
}
