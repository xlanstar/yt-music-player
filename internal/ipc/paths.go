package ipc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
)

// appName names the app's directories, as in auth and settings.
const appName = "yt-music-player"

// ErrPathTooLong is returned when even the short fallback socket path is
// longer than the OS allows.
var ErrPathTooLong = errors.New("ipc: socket path too long")

// Roots are the OS directories the paths derive from.
type Roots struct {
	Config string // os.UserConfigDir
	Cache  string // os.UserCacheDir: %LOCALAPPDATA% on Windows
	// Runtime is $XDG_RUNTIME_DIR, read on Linux only; empty when unset.
	Runtime string
	// Temp holds the socket when Runtime is unset, or the usual path is
	// too long: /tmp on Linux, so that every process agrees whatever its
	// $TMPDIR; os.TempDir elsewhere ($TMPDIR, per user on macOS).
	Temp string
	UID  int // os.Getuid; -1 on Windows
}

// Paths are where ytmd keeps its files. Each name carries the build's
// suffix: "-placeholder" for a placeholder build, then "-dev" for a
// development one, so neither touches an installed daemon's files.
type Paths struct {
	Socket string
	Lock   string // next to Socket
	State  string // daemon.json
	Logs   string // the directory of the log files (Log)

	suffix string
	uid    int
	// shared reports the socket's directory in a directory others write
	// to (/tmp): someone else may have made it first.
	shared bool
}

// Default returns the paths in the OS's directories; dev reports a
// development build.
func Default(dev bool) (Paths, error) {
	config, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, fmt.Errorf("ipc: %w", err)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return Paths{}, fmt.Errorf("ipc: %w", err)
	}
	r := Roots{Config: config, Cache: cache, Temp: os.TempDir(), UID: os.Getuid()}
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		r.Runtime = os.Getenv("XDG_RUNTIME_DIR")
		r.Temp = "/tmp"
	}
	return Resolve(r, dev)
}

// Resolve returns the paths under r; it touches no file.
func Resolve(r Roots, dev bool) (Paths, error) {
	return resolve(r, runtime.GOOS, suffix(isPlaceholder, dev))
}

func suffix(placeholder, dev bool) string {
	s := ""
	if placeholder {
		s += "-placeholder"
	}
	if dev {
		s += "-dev"
	}
	return s
}

func resolve(r Roots, goos, suffix string) (Paths, error) {
	p := Paths{
		State:  filepath.Join(r.Config, appName, "daemon"+suffix+".json"),
		Logs:   filepath.Join(r.Cache, appName, "logs"),
		suffix: suffix,
		uid:    r.UID,
	}
	sock := "ytmd" + suffix + ".sock"
	var dir string
	switch {
	case goos == "windows":
		dir = filepath.Join(r.Cache, appName)
	case goos == "darwin":
		dir = filepath.Join(r.Config, appName)
	case r.Runtime != "":
		dir = filepath.Join(r.Runtime, appName)
	}
	if dir == "" || len(filepath.Join(dir, sock)) > maxSocketPath(goos) {
		dir = filepath.Join(r.Temp, appName)
		if r.UID >= 0 {
			dir += "-" + strconv.Itoa(r.UID)
		}
		p.shared = goos != "windows"
	}
	p.Socket = filepath.Join(dir, sock)
	if len(p.Socket) > maxSocketPath(goos) {
		return Paths{}, fmt.Errorf("%w: %s", ErrPathTooLong, p.Socket)
	}
	p.Lock = filepath.Join(dir, "ytmd"+suffix+".lock")
	return p, nil
}

// maxSocketPath is the longest socket path in bytes: sun_path less its NUL.
func maxSocketPath(goos string) int {
	if goos == "darwin" || goos == "ios" || goos == "freebsd" || goos == "netbsd" || goos == "openbsd" {
		return 103
	}
	return 107
}

// Log returns the path of name's log, e.g. Log("ytmd") or Log("ytm").
func (p Paths) Log(name string) string {
	return filepath.Join(p.Logs, name+p.suffix+".log")
}

// Prepare makes the socket's and the state's directories, the socket's
// private (0700, the user's own; in /tmp, one someone else made is
// refused). The log's makes its Writer (package logfile).
func (p Paths) Prepare() error {
	if err := privateDir(filepath.Dir(p.Socket), p.uid, p.shared); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.State), 0o700); err != nil {
		return fmt.Errorf("ipc: %w", err)
	}
	return nil
}
