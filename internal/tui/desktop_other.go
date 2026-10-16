//go:build !darwin

package tui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"yt-music-player/internal/ipc"
)

// errNoDesktop is a Desktop app found neither next to ytm nor on PATH.
var errNoDesktop = errors.New("tui: the Desktop app was not found")

// openDesktop starts the Desktop app, detached: the one next to ytm, as
// the packages install them, else the one on PATH.
func openDesktop() error {
	path, err := findDesktop()
	if err != nil {
		return err
	}
	return ipc.Spawn(path)
}

// desktopName is the Desktop app's executable on this OS.
func desktopName() string {
	if runtime.GOOS == "windows" {
		return "YT Music Player.exe"
	}
	return "yt-music-player"
}

func findDesktop() (string, error) {
	name := desktopName()
	if exe, err := os.Executable(); err == nil {
		if exe, err = filepath.EvalSymlinks(exe); err == nil {
			path := filepath.Join(filepath.Dir(exe), name)
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
				return path, nil
			}
		}
	}
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	return "", errNoDesktop
}
