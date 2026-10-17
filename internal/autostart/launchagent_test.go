package autostart

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Paths with what XML and sh would each take for their own.
const (
	oddHome = `/Users/a & b/<YT> "it's" $HOME 音樂`
	oddYtmd = `/Volumes/a & b/<YT> "Music" 'Player' $(false) 音樂/ytmd`
)

func TestPlistEscapes(t *testing.T) {
	path := filepath.Join(oddHome, "Library", "LaunchAgents", Label+".plist")
	data := renderPlist(path, oddYtmd)
	d := xml.NewDecoder(bytes.NewReader(data))
	for {
		_, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("plist is not XML: %v\n%s", err, data)
		}
	}
	want := []string{
		"/bin/sh",
		"-c",
		`[ -x "$0" ] && exec "$0"; rm -f '/Users/a & b/<YT> "it'\''s" $HOME 音樂/Library/LaunchAgents/` +
			Label + `.plist'; launchctl disable gui/$(id -u)/` + Label,
		oddYtmd,
	}
	if got := plistArgs(data); !slices.Equal(got, want) {
		t.Errorf("ProgramArguments = %q, want %q", got, want)
	}
	if got := plistProgram(data); got != oddYtmd {
		t.Errorf("plistProgram = %q, want %q", got, oddYtmd)
	}
}

// TestPlistLints has macOS's own parser read the plist, where it is.
func TestPlistLints(t *testing.T) {
	plutil, err := exec.LookPath("plutil")
	if err != nil {
		t.Skip("no plutil")
	}
	file := filepath.Join(t.TempDir(), "agent.plist")
	if err := os.WriteFile(file, renderPlist(filepath.Join(oddHome, Label+".plist"), oddYtmd), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(t.Context(), plutil, "-lint", file).CombinedOutput(); err != nil {
		t.Errorf("plutil -lint: %v\n%s", err, out)
	}
}

func TestPlistProgram(t *testing.T) {
	for data, want := range map[string]string{
		oldPlist:  appYtmd,
		"":        "",
		"<plist>": "",
		"<plist><dict><key>Label</key><string>x</string></dict></plist>": "",
		// Strings of other keys are not the program.
		"<plist><dict><key>AssociatedBundleIdentifiers</key><array><string>x</string></array></dict></plist>": "",
		// sh without the arguments renderPlist gives it starts no ytmd.
		"<plist><dict><key>ProgramArguments</key><array><string>/bin/sh</string><string>-c</string></array></dict></plist>": "",
	} {
		if got := plistProgram([]byte(data)); got != want {
			t.Errorf("plistProgram(%q) = %q, want %q", data, got, want)
		}
	}
}

// TestPlistUpgrades holds that the front-end's Repair at start rewrites a
// plist an older version wrote, which starts ytmd directly, into one that
// removes itself.
func TestPlistUpgrades(t *testing.T) {
	home := t.TempDir()
	var c commands
	a := newLaunchAgent(home, 501, c.run)
	if err := writeFile(a.path, []byte(oldPlist)); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, a, true, appYtmd)
	if err := a.repair(appYtmd); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, a.path), strings.ReplaceAll(wantPlist, "@HOME@", home); got != want {
		t.Errorf("Repair wrote\n%s\nwant\n%s", got, want)
	}
	wantStatus(t, a, true, appYtmd)
	wantCommands(t, &c)
}

// TestPlistScript runs the plist's ProgramArguments as launchd would, with
// a launchctl that records its arguments.
func TestPlistScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /bin/sh")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	launchctlLog := filepath.Join(dir, "launchctl.log")
	ytmdLog := filepath.Join(dir, "ytmd.log")
	writeScript(t, filepath.Join(bin, "launchctl"), `printf '%s\n' "$@" >"$LAUNCHCTL_LOG"`)
	ytmd := filepath.Join(dir, `My Apps "x" 'y' $HOME 音樂`, "ytmd")
	writeScript(t, ytmd, `printf '%s' "$0" >"$YTMD_LOG"`)

	var c commands
	a := newLaunchAgent(filepath.Join(dir, `home 'it's' "$USER"`), os.Getuid(), c.run)
	if err := a.enable(ytmd); err != nil {
		t.Fatal(err)
	}
	args := plistArgs([]byte(readFile(t, a.path)))
	login := func() {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), args[0], args[1:]...)
		cmd.Env = []string{
			"PATH=" + bin + ":/usr/bin:/bin",
			// Not the agent's home: the script names the plist itself.
			"HOME=" + filepath.Join(dir, "elsewhere"),
			"LAUNCHCTL_LOG=" + launchctlLog,
			"YTMD_LOG=" + ytmdLog,
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%q: %v\n%s", args, err, out)
		}
	}

	// ytmd present: exec'd, the plist kept, launchctl untouched.
	login()
	if got := readFile(t, ytmdLog); got != ytmd {
		t.Errorf("ytmd ran as %q, want %q", got, ytmd)
	}
	if _, err := os.Stat(a.path); err != nil {
		t.Errorf("plist gone with ytmd present: %v", err)
	}
	if _, err := os.Stat(launchctlLog); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("launchctl ran with ytmd present: %v", err)
	}

	// ytmd gone with the app: the plist removed and the label disabled.
	if err := os.Remove(ytmd); err != nil {
		t.Fatal(err)
	}
	login()
	if _, err := os.Stat(a.path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("plist left without ytmd: %v", err)
	}
	if got, want := readFile(t, launchctlLog), "disable\ngui/"+strconv.Itoa(os.Getuid())+"/"+Label+"\n"; got != want {
		t.Errorf("launchctl ran with %q, want %q", got, want)
	}
	wantStatus(t, a, false, "")
}

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}
