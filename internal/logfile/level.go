package logfile

import (
	"fmt"
	"strings"

	"github.com/rs/zerolog"
)

// Level reads the level YT_LOG names: debug, info, warn (the default,
// also for an empty name) or error. An unknown name is warn, with an
// error to log.
func Level(name string) (zerolog.Level, error) {
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
