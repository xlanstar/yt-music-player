package main

import (
	"path/filepath"
	"yt-music-player/internal/version"
	"yt-music-player/locales"

	"github.com/rs/zerolog/log"
)

// autostartCommand is `ytm autostart on|off|status`. Switching decides the
// next login only: a daemon running goes on running.
func (e *env) autostartCommand(args []string) int {
	if len(args) != 1 {
		return e.usage("autostart")
	}
	what := locales.CliAutostartFailed()
	switch args[0] {
	case "on":
		ytmd, err := e.daemonPath()
		if err == nil {
			err = e.autostart.enable(ytmd)
		}
		if err != nil {
			return e.fail(what, err)
		}
		writeLine(e.stdout, locales.CliAutostartOn())
	case "off":
		if err := e.autostart.disable(); err != nil {
			return e.fail(what, err)
		}
		writeLine(e.stdout, locales.CliAutostartOff())
	case "status":
		enabled, _, err := e.autostart.status()
		switch {
		case err != nil:
			return e.fail(what, err)
		case enabled:
			writeLine(e.stdout, locales.CliAutostartOn())
		default:
			writeLine(e.stdout, locales.CliAutostartOff())
		}
	default:
		return e.usage("autostart")
	}
	return exitOK
}

// daemonPath returns the absolute path of this ytm's own ytmd: login
// starts it from anywhere.
func (e *env) daemonPath() (string, error) {
	ytmd, err := e.findDaemon()
	if err != nil {
		return "", err
	}
	return filepath.Abs(ytmd)
}

// repairAutostart points the login entry, when on, at this ytm's ytmd, as
// after the app moved (docs/daemon.md, Start at login), on its own goroutine:
// the returned channel closes when it is done. It never fails a command,
// and a development build never repairs, which would point the user's
// login at it.
func (e *env) repairAutostart() <-chan struct{} {
	done := make(chan struct{})
	if version.IsDev(e.version) {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		ytmd, err := e.daemonPath()
		if err == nil {
			err = e.autostart.repair(ytmd)
		}
		if err != nil {
			log.Debug().Err(err).Msg("ytm: repairing autostart")
		}
	}()
	return done
}
