package main

import (
	"fmt"
	"strings"

	"github.com/rs/zerolog"
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
