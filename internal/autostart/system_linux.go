package autostart

import (
	"fmt"
	"os"
)

func current() (item, error) {
	// XDG_CONFIG_HOME, or ~/.config.
	config, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("autostart: %w", err)
	}
	return newSystemdUnit(config, execRun), nil
}
