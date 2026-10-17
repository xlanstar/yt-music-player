// Package autostart starts the daemon at login, or stops doing so: a
// LaunchAgent on macOS, a systemd user unit on Linux, a value of HKCU's Run
// key on Windows. Switching decides only the next login, never starts or
// stops a daemon now, so turning it off does not stop the music. The app
// may move, so front-ends call Repair at start; deleting it turns the
// LaunchAgent off and has systemd skip the unit (the Run value stays).
//
// On Windows it also puts the install directory on the user's Path
// (EnablePath), so terminals find ytm: MyGo's installer cannot.
//
// Calls run commands or touch the registry: make them off the UI thread.
// The logic runs on every OS over fakes; system_*.go picks this OS's parts.
package autostart

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// label names the LaunchAgent, and the daemon's bundle identifier.
	label    = "dev.lanstar.ytmusicplayer.daemon"
	unitName = "ytmd.service"
	// runValue names the value under HKCU's Run key.
	runValue = "YTMusicPlayerDaemon"
)

// item is one platform's way to start the daemon at login.
type item interface {
	enable(ytmd string) error
	disable() error
	status() (enabled bool, err error)
	// repair rewrites the entry when it is on and differs from what enable
	// would write now.
	repair(ytmd string) error
}

// Enable starts ytmd, an absolute path, at the next logins.
func Enable(ytmd string) error {
	if err := checkPath(ytmd); err != nil {
		return err
	}
	it, err := current()
	if err != nil {
		return err
	}
	return it.enable(ytmd)
}

// Disable stops starting the daemon at login; turning it off when it is off
// does nothing.
func Disable() error {
	it, err := current()
	if err != nil {
		return err
	}
	return it.disable()
}

// Status reports whether the daemon starts at login.
func Status() (enabled bool, err error) {
	it, err := current()
	if err != nil {
		return false, err
	}
	return it.status()
}

// Repair points the entry at ytmd when it is on and points elsewhere, as
// after the app moved; it never turns the entry on.
func Repair(ytmd string) error {
	if err := checkPath(ytmd); err != nil {
		return err
	}
	it, err := current()
	if err != nil {
		return err
	}
	return it.repair(ytmd)
}

// checkPath refuses what no entry can hold, and a relative path, which
// would start from wherever login does.
func checkPath(p string) error {
	if !filepath.IsAbs(p) {
		return fmt.Errorf("autostart: %q is not an absolute path", p)
	}
	if !utf8.ValidString(p) || strings.ContainsFunc(p, unicode.IsControl) {
		return fmt.Errorf("autostart: %q has control characters or invalid UTF-8", p)
	}
	return nil
}

// runner runs a command to completion; tests record its argv instead.
type runner func(name string, args ...string) error

// writeFile replaces path with data through a rename, so login never reads
// half an entry.
func writeFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("autostart: %w", err)
	}
	f, err := os.CreateTemp(dir, ".autostart-*")
	if err != nil {
		return fmt.Errorf("autostart: %w", err)
	}
	_, err = f.Write(data)
	if err == nil {
		// CreateTemp's 0600, made the mode service managers' own files have
		// (launchd refuses a plist others may write).
		err = f.Chmod(0o644)
	}
	err = errors.Join(err, f.Close())
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return fmt.Errorf("autostart: %w", err)
	}
	return nil
}

// removeFile removes path; one already gone is no error.
func removeFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("autostart: %w", err)
	}
	return nil
}

// fileEntry is the entry of a platform whose entry is a file the service
// manager is then told about.
type fileEntry struct {
	path   string
	render func(ytmd string) []byte
	run    runner
	// on and off are the commands that switch the entry, without starting
	// or stopping anything now.
	on, off []string
}

func (e fileEntry) enable(ytmd string) error {
	if err := writeFile(e.path, e.render(ytmd)); err != nil {
		return err
	}
	if err := e.run(e.on[0], e.on[1:]...); err != nil {
		// Status reads the file: without it, the entry reads as off, as
		// the service manager has it.
		return errors.Join(err, removeFile(e.path))
	}
	return nil
}

// disable removes the file even when the command fails: without it nothing
// starts at login, whatever the service manager still lists.
func (e fileEntry) disable() error {
	if _, err := os.Stat(e.path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	err := e.run(e.off[0], e.off[1:]...)
	return errors.Join(err, removeFile(e.path))
}

// status reads the file, as the service manager reads it at login.
func (e fileEntry) status() (bool, error) {
	data, err := e.read()
	return data != nil, err
}

// read returns the entry's file, or nil when the entry is off.
func (e fileEntry) read() ([]byte, error) {
	data, err := os.ReadFile(e.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("autostart: %w", err)
	}
	return data, nil
}

// repair compares the whole file, so an entry an older version wrote is
// brought up to date too. The service manager reads the file at the next
// login; nothing needs telling.
func (e fileEntry) repair(ytmd string) error {
	data, err := e.read()
	if err != nil || data == nil {
		return err
	}
	want := e.render(ytmd)
	if bytes.Equal(data, want) {
		return nil
	}
	return writeFile(e.path, want)
}
