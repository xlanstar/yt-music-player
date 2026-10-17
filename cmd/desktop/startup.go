package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"yt-music-player/internal/autostart"
	"yt-music-player/internal/ipc"
	"yt-music-player/internal/job"
	"yt-music-player/internal/notice"

	"github.com/rs/zerolog/log"
)

// startupState is the settings' Background player: whether ytmd starts at
// login (docs/daemon.md, Start at login), and on Windows whether
// terminals find ytm. Each switch shows what was last read or set; switching
// runs package autostart, which may take seconds, off the UI thread, and
// a failure turns the switch back.
type startupState struct {
	// atLogin is the switch's: ytmd starts at login. isKnown: Status
	// answered.
	atLogin, isKnown bool
	login            job.Job
	// inTerminal is the switch's: the app's folder is on the Path, as
	// settings.TerminalPath says.
	inTerminal bool
	terminal   job.Job
	// hasTerminal offers the switch: Windows only, set before
	// startStartup.
	hasTerminal bool
	sys         startupSystem
}

// startupSystem is package autostart and where the helpers are, a fake's
// in tests.
type startupSystem struct {
	enable      func(ytmd string) error
	disable     func() error
	status      func() (enabled bool, err error)
	repair      func(ytmd string) error
	enablePath  func(dir string) error
	disablePath func(dir string) error
	repairPath  func(old, dir string) error
	// daemon is the path of this app's ytmd, ytmDir the folder of its ytm.
	daemon func() (string, error)
	ytmDir func() (string, error)
}

// systemStartup is this system's.
func systemStartup() startupSystem {
	return startupSystem{
		enable:      autostart.Enable,
		disable:     autostart.Disable,
		status:      autostart.Status,
		repair:      autostart.Repair,
		enablePath:  autostart.EnablePath,
		disablePath: autostart.DisablePath,
		repairPath:  autostart.RepairPath,
		daemon:      ipc.FindDaemon,
		ytmDir:      ytmDir,
	}
}

var errNoYtm = errors.New("no ytm beside the app")

// ytmDir is the folder of the ytm shipped beside the app.
func ytmDir() (string, error) {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(exe)
	name := ytmCommand
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
		return "", errNoYtm
	}
	return dir, nil
}

// startStartup reads whether ytmd starts at login and, unless dev, points
// what is on at this app's helpers, as after the app moved or updated: a
// development build would point the user's login at itself. Call it once
// the settings are loaded.
func (a *app) startStartup(sys startupSystem, dev bool) {
	st := &a.startup
	st.sys = sys
	st.inTerminal = a.look.set.TerminalPath != ""
	oldPath, hasTerminal := a.look.set.TerminalPath, st.hasTerminal
	type found struct {
		atLogin bool
		path    string
	}
	job.Run(&st.login, a.post, func(context.Context) (found, error) {
		enabled, err := sys.status()
		if err != nil || dev {
			return found{atLogin: enabled}, err
		}
		f := found{atLogin: enabled, path: oldPath}
		if enabled {
			if err := repairLogin(sys); err != nil {
				log.Debug().Err(err).Msg("repairing the login item")
			}
		}
		if oldPath != "" && hasTerminal {
			if dir, err := sys.ytmDir(); err == nil && dir != oldPath {
				if err := sys.repairPath(oldPath, dir); err != nil {
					log.Debug().Err(err).Msg("repairing the Path")
				} else {
					f.path = dir
				}
			}
		}
		return f, nil
	}, func(f found, err error) {
		if err != nil {
			log.Debug().Err(err).Msg("reading the login item")
		}
		// Unread, the switch shows off: turning it on says what fails.
		st.atLogin, st.isKnown = f.atLogin, true
		if f.path != "" && f.path != a.look.set.TerminalPath {
			a.look.set.TerminalPath = f.path
			a.look.saver.Save(a.look.set)
		}
	})
}

func repairLogin(sys startupSystem) error {
	ytmd, err := daemonPath(sys)
	if err != nil {
		return err
	}
	return sys.repair(ytmd)
}

// daemonPath is the absolute path of this app's ytmd: login starts it from
// anywhere.
func daemonPath(sys startupSystem) (string, error) {
	ytmd, err := sys.daemon()
	if err != nil {
		return "", err
	}
	return filepath.Abs(ytmd)
}

// setAtLogin has ytmd start at login, or not; the daemon running plays on
// either way.
func (a *app) setAtLogin(on bool) {
	st := &a.startup
	sys := st.sys
	job.Run(&st.login, a.post, func(context.Context) (struct{}, error) {
		if !on {
			return struct{}{}, sys.disable()
		}
		ytmd, err := daemonPath(sys)
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, sys.enable(ytmd)
	}, func(_ struct{}, err error) {
		if err != nil {
			st.atLogin = !on
			a.tellLater(switchFailed(notice.AutostartFailed, err))
		}
	})
}

// setInTerminal puts the folder of the app's ytm on the user's Path, or
// takes the one it put there off, and keeps which in the settings.
func (a *app) setInTerminal(on bool) {
	st := &a.startup
	sys, old := st.sys, a.look.set.TerminalPath
	job.Run(&st.terminal, a.post, func(context.Context) (string, error) {
		if !on {
			if old == "" {
				return "", nil
			}
			return "", sys.disablePath(old)
		}
		dir, err := sys.ytmDir()
		if err != nil {
			return "", err
		}
		return dir, sys.enablePath(dir)
	}, func(dir string, err error) {
		if err != nil {
			st.inTerminal = !on
			a.tellLater(switchFailed(notice.TerminalFailed, err))
			return
		}
		a.look.set.TerminalPath = dir
		a.look.saver.Save(a.look.set)
	})
}

func switchFailed(title notice.Code, err error) notice.Notice {
	return notice.Notice{Kind: notice.Failure, Title: title, Detail: notice.Reason, Args: []string{err.Error()}}
}
