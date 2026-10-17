package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
	"yt-music-player/internal/api"

	"github.com/rs/zerolog/log"
)

// daemonBundle is the macOS bundle ytmd runs in (scripts/build.sh),
// whose name Now Playing shows. Not the app's: LaunchServices registers
// nested apps too, and a second, windowless "YT Music Player" would answer
// open -a and launchers.
const daemonBundle = "YT Music Player Helper.app"

// FindDaemon returns the path of ytmd: $YTMD_PATH, else the one shipped
// with this program; api.ErrDaemonNotFound when there is none.
func FindDaemon() (string, error) {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return "", fmt.Errorf("%w: %w", api.ErrDaemonNotFound, err)
	}
	return findDaemon(exe, runtime.GOOS)
}

// findDaemon is FindDaemon for the program at exe, on goos.
func findDaemon(exe, goos string) (string, error) {
	if path := os.Getenv("YTMD_PATH"); path != "" {
		if !isFile(path) {
			return "", fmt.Errorf("%w: YTMD_PATH %s is no file", api.ErrDaemonNotFound, path)
		}
		return path, nil
	}
	for _, path := range daemonCandidates(filepath.Dir(exe), goos) {
		if isFile(path) {
			return path, nil
		}
	}
	return "", api.ErrDaemonNotFound
}

// daemonCandidates are where ytmd ships next to a program in dir, in the
// order looked at.
func daemonCandidates(dir, goos string) []string {
	if goos == "windows" {
		return []string{filepath.Join(dir, "ytmd.exe")}
	}
	paths := []string{filepath.Join(dir, "ytmd")}
	if goos == "darwin" {
		paths = append(paths,
			// ytm, in Contents/Resources beside the bundle
			filepath.Join(dir, daemonBundle, "Contents", "MacOS", "ytmd"),
			// Desktop, in Contents/MacOS
			filepath.Join(dir, "..", "Resources", daemonBundle, "Contents", "MacOS", "ytmd"),
		)
	}
	return paths
}

func isFile(path string) bool {
	info, err := os.Stat(path) //nolint:gosec // $YTMD_PATH, the user's own choice
	return err == nil && info.Mode().IsRegular()
}

// Spawn starts the daemon at path detached from this process: in its own
// session or process group, its standard streams on the null device, with
// this process's environment (YT_LOG too).
func Spawn(path string) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("ipc: %w", err)
	}
	cmd := exec.Command(path) //nolint:noctx // the daemon outlives any context here
	// Not this process's directory, which the daemon would keep busy.
	cmd.Dir = filepath.Dir(path)
	cmd.SysProcAttr = detached()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ipc: start ytmd: %w", err)
	}
	// Reaps it: one that exits at once, another holding the lock, would
	// stay a zombie as long as this process. The goroutine blocks in the
	// OS, waking for nothing until then.
	go func() { _ = cmd.Wait() }()
	return nil
}

// SpawnDaemon finds ytmd and starts it.
func SpawnDaemon() error {
	path, err := FindDaemon()
	if err != nil {
		return err
	}
	return Spawn(path)
}

// Dialer connects to ytmd's socket, starting ytmd if asked to.
type Dialer struct {
	Socket string
	// Spawn starts ytmd; nil is SpawnDaemon.
	Spawn func() error

	// every is the wait between dials, 50 ms when 0; timeout how long
	// Wait dials for, 5 s when 0. Tests shorten them.
	every, timeout time.Duration
}

// Dial connects once, never starting ytmd.
func (d *Dialer) Dial(ctx context.Context) (net.Conn, error) {
	return Dial(ctx, d.Socket)
}

// Wait dials every 50 ms until it connects, or fails with
// api.ErrDaemonUnavailable after 5 s; it never starts ytmd.
func (d *Dialer) Wait(ctx context.Context) (net.Conn, error) {
	every, timeout := d.every, d.timeout
	if every <= 0 {
		every = 50 * time.Millisecond
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, api.ErrDaemonUnavailable)
	defer cancel()
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		c, err := Dial(ctx, d.Socket)
		if err == nil {
			return c, nil
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			if cause := context.Cause(ctx); errors.Is(cause, api.ErrDaemonUnavailable) {
				return nil, fmt.Errorf("%w: %w", cause, err)
			}
			return nil, ctx.Err()
		}
	}
}

// DialOrSpawn connects, or else starts ytmd, whatever the failure (the
// lock makes a needless start harmless), and Waits for it.
func (d *Dialer) DialOrSpawn(ctx context.Context) (net.Conn, error) {
	c, err := Dial(ctx, d.Socket)
	if err == nil {
		return c, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	log.Debug().Err(err).Msg("ipc: no daemon; starting one")
	spawn := d.Spawn
	if spawn == nil {
		spawn = SpawnDaemon
	}
	if err := spawn(); err != nil {
		log.Debug().Err(err).Msg("ipc: could not start the daemon")
		return nil, err
	}
	return d.Wait(ctx)
}
