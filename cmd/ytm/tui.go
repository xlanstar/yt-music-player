package main

import (
	"context"
	"yt-music-player/locales"
)

// runTUI runs the player in the terminal, `ytm` without arguments, until
// the user quits, and returns the exit code. The language is set already
// (useLanguage), and zerolog writes to ytm's log file, never the terminal.
// The TUI (internal/tui) replaces this file; until then it says it is
// not there yet.
func runTUI(_ context.Context, e *env) int {
	writeLine(e.stderr, locales.CliTuiUnavailable())
	return exitFailed
}
