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

// startupState is the settings' Background player: ytmd at login and, on
// Windows, ytm on the Path. Switching runs package autostart, which may take
// seconds, off the UI thread; a failure turns the switch back.
type startupState struct {
	login    job.Job
	terminal job.Job
	sys      startupSystem
	// isKnown is set once the login status request has answered.
	atLogin, isKnown bool
	inTerminal       bool
	// hasTerminal enables the Windows Path switch; set before startStartup.
	hasTerminal bool
}

// startupSystem is package autostart and where the helpers are; tests fake
// it.
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

// startStartup reads whether ytmd starts at login and, unless dev (which
// would point the user's login at itself), points what is on at this app's
// helpers, as after the app moved. Call it once the settings are loaded.
func (a *app) startStartup(sys startupSystem, dev bool) {
	st := &a.startup
	st.sys = sys
	st.inTerminal = a.look.set.TerminalPath != ""
	oldPath, hasTerminal := a.look.set.TerminalPath, st.hasTerminal
	type found struct {
		atLogin bool
		// movedPath is the Path's new folder, "" when unchanged.
		movedPath string
	}
	job.Run(&st.login, a.post, func(context.Context) (found, error) {
		enabled, err := sys.status()
		f := found{atLogin: enabled}
		if err != nil || dev {
			return f, err
		}
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
					f.movedPath = dir
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
		if f.movedPath != "" {
			a.look.set.TerminalPath = f.movedPath
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
	return notice.Notice{Kind: notice.KindFailure, Title: title, Detail: notice.Reason, Args: []string{err.Error()}}
}
