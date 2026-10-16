package main

import (
	"io"

	"github.com/rs/zerolog/log"
)

// logTo sends the global logger's lines to w: ytm's file, a test's sink.
// Call it once, before anything logs.
func logTo(w io.Writer) {
	log.Logger = log.Logger.Output(w) //nolint:forbidigo // ytm logs to its file, not the terminal the TUI owns (docs/plans/daemon.md §6.5)
}
