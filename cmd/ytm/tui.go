package main

import (
	"context"
	"yt-music-player/internal/tui"
	"yt-music-player/locales"
)

// runTUI runs the player in the terminal, `ytm` without arguments, until
// the user quits, and returns the exit code. The language is set already
// (useLanguage), and zerolog writes to ytm's log file, never the terminal.
// Quitting leaves the daemon playing.
func runTUI(ctx context.Context, e *env) int {
	err := tui.Run(ctx, tui.Config{
		Dialer:       e.dialer,
		Spawn:        e.spawn,
		Version:      e.version,
		DaemonLog:    e.paths.Log("ytmd"),
		AccountCache: e.paths.AccountCache,
	})
	if err != nil {
		writeLine(e.stderr, locales.CliFailed(locales.CliTuiFailed(), err.Error()))
		return exitFailed
	}
	return exitOK
}
