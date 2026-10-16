package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// logLevel returns warn, the default, with an error for an unknown name.
func logLevel(name string) (zerolog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return zerolog.DebugLevel, nil
	case "info":
		return zerolog.InfoLevel, nil
	case "", "warn":
		return zerolog.WarnLevel, nil
	case "error":
		return zerolog.ErrorLevel, nil
	}
	return zerolog.WarnLevel, fmt.Errorf("unknown level %q: want debug, info, warn or error", name)
}

// logTo sends the global logger's lines to w: ytm's file, a test's sink.
// Call it once, before anything logs.
func logTo(w io.Writer) {
	log.Logger = log.Logger.Output(w) //nolint:forbidigo // ytm logs to its file, not the terminal the TUI owns (docs/plans/daemon.md §6.5)
}
