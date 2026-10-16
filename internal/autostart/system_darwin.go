package autostart

import (
	"fmt"
	"os"
)

func current() (item, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("autostart: %w", err)
	}
	return newLaunchAgent(home, os.Getuid(), execRun), nil
}
