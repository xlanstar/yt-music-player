// Command ytm is YT Music Player in the terminal: without arguments the
// player (package tui), otherwise one command to ytmd (docs/daemon.md).
//
// A command connects once and never restarts an older daemon, so that a
// script's `ytm next` never interrupts the music. People read words from
// locales; scripts read status --json, versions and paths, untranslated.
// ytm logs to its own file, never to the terminal, which the TUI owns.
//
// run reaches outside the process only through an env, so tests run
// commands against a fake daemon.
package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/autostart"
	"yt-music-player/internal/ipc"
	"yt-music-player/internal/logfile"
	"yt-music-player/internal/remote"
	"yt-music-player/internal/version"
	"yt-music-player/locales"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"golang.org/x/term"
)

// The exit codes.
const (
	exitOK      = 0
	exitFailed  = 1
	exitUsage   = 2
	exitStopped = 3 // status and `daemon status`: no daemon runs
)

// env is what run reads and changes outside itself: main's are the
// process's own, tests' are fakes.
type env struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	// isTerminal reports stdin a terminal; rawInput puts it in raw mode
	// (no echo, no line editing) until restore.
	isTerminal func() bool
	rawInput   func() (restore func(), err error)

	paths ipc.Paths
	// dialer reaches the daemon; its DialOrSpawn starts one through spawn.
	dialer remote.Dialer
	// spawn starts this ytm's own ytmd (`daemon restart`).
	spawn func() error
	// findDaemon returns the path of that ytmd (autostart).
	findDaemon func() (string, error)
	autostart  autostarter
	// version is this ytm's.
	version string
}

// autostarter is package autostart's switch, a fake's in tests.
type autostarter struct {
	enable  func(ytmd string) error
	disable func() error
	status  func() (enabled bool, err error)
	repair  func(ytmd string) error
}

func main() {
	os.Exit(mainCode())
}

// mainCode is main without os.Exit, so that deferred calls run.
func mainCode() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	paths, err := ipc.Default(version.IsDev(version.Version))
	if err == nil {
		closeLog := startLog(paths)
		defer closeLog()
	} else {
		// Never on the terminal.
		zerolog.SetGlobalLevel(zerolog.Disabled)
	}
	useLanguage()
	if err != nil {
		writeLine(os.Stderr, locales.CliFailed(locales.CliNoPaths(), err.Error()))
		return exitFailed
	}
	return run(ctx, os.Args[1:], processEnv(paths))
}

// processEnv returns the env of this process.
func processEnv(paths ipc.Paths) *env {
	fd := int(os.Stdin.Fd())
	return &env{
		stdin:      os.Stdin,
		stdout:     os.Stdout,
		stderr:     os.Stderr,
		isTerminal: func() bool { return term.IsTerminal(fd) },
		rawInput: func() (func(), error) {
			old, err := term.MakeRaw(fd)
			if err != nil {
				return nil, err
			}
			return func() { _ = term.Restore(fd, old) }, nil
		},
		paths:      paths,
		dialer:     &ipc.Dialer{Socket: paths.Socket, Spawn: ipc.SpawnDaemon},
		spawn:      ipc.SpawnDaemon,
		findDaemon: ipc.FindDaemon,
		autostart: autostarter{
			enable:  autostart.Enable,
			disable: autostart.Disable,
			status:  autostart.Status,
			repair:  autostart.Repair,
		},
		version: version.Version,
	}
}

// startLog sends zerolog to ytm's log file at YT_LOG's level, and returns
// what closes the file.
func startLog(paths ipc.Paths) func() {
	level, levelErr := logfile.Level(os.Getenv("YT_LOG"))
	zerolog.SetGlobalLevel(level)
	w := logfile.New(paths.Log("ytm"))
	logTo(w)
	if levelErr != nil {
		log.Warn().Err(levelErr).Msg("YT_LOG")
	}
	return func() { _ = w.Close() }
}

// run runs the command of args and returns its exit code.
func run(ctx context.Context, args []string, e *env) int {
	if len(args) > 0 && args[0] == "autostart" {
		// It switches the entry itself.
		return e.autostartCommand(args[1:])
	}
	repaired := e.repairAutostart()
	defer func() {
		select {
		case <-repaired:
		case <-time.After(repairWait):
		}
	}()
	if len(args) == 0 {
		return runTUI(ctx, e)
	}
	name, rest := args[0], args[1:]
	switch name {
	case "help", "-h", "--help":
		e.help()
		return exitOK
	case "status":
		return e.statusCommand(ctx, rest)
	case "play", "pause", "toggle", "next", "prev":
		if len(rest) > 0 {
			return e.usage(name)
		}
		return e.call(ctx, api.Call{Method: controls[name]})
	case "seek":
		return e.seekCommand(ctx, rest)
	case "volume":
		return e.volumeCommand(ctx, rest)
	case "login":
		if len(rest) > 0 {
			return e.usage(name)
		}
		return e.login(ctx)
	case "logout":
		if len(rest) > 0 {
			return e.usage(name)
		}
		return e.logout(ctx)
	case "daemon":
		return e.daemonCommand(ctx, rest)
	case "logs":
		if len(rest) > 0 {
			return e.usage(name)
		}
		return e.logs()
	case "version":
		if len(rest) > 0 {
			return e.usage(name)
		}
		return e.versionCommand(ctx)
	}
	writeLine(e.stderr, locales.CliUnknownCommand(name))
	return exitUsage
}

// controls are the methods of the commands without arguments.
var controls = map[string]api.Method{
	"play":   api.MethodResume,
	"pause":  api.MethodPause,
	"toggle": api.MethodToggle,
	"next":   api.MethodNext,
	"prev":   api.MethodPrev,
}

// writeLine writes s and a newline to w. A failed write, to a terminal or
// pipe gone, has nobody left to tell.
func writeLine(w io.Writer, s string) { _, _ = io.WriteString(w, s+"\n") }
