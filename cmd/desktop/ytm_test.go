package main

import (
	"path/filepath"
	"testing"
)

func TestYtmTarget(t *testing.T) {
	deb := filepath.Join("/opt", "yt-music-player", "yt-music-player")
	local := filepath.Join("/home", "u", ".local", "yt-music-player.app", "yt-music-player")
	tests := []struct {
		name, arg0, exe, want string
	}{
		{"deb command", "ytm", deb, filepath.Join("/opt", "yt-music-player", "ytm")},
		{"by path", filepath.Join("/usr", "bin", "ytm"), deb, filepath.Join("/opt", "yt-music-player", "ytm")},
		{"install.sh command", filepath.Join("/home", "u", ".local", "bin", "ytm"), local, filepath.Join("/home", "u", ".local", "yt-music-player.app", "ytm")},
		{"the app", "yt-music-player", deb, ""},
		{"desktop entry", deb, deb, ""},
		{"ytm in its name", "ytm-dev", deb, ""},
		{"ytm itself", "ytm", filepath.Join("/opt", "yt-music-player", "ytm"), ""},
	}
	for _, tt := range tests {
		if got := ytmTarget(tt.arg0, tt.exe); got != tt.want {
			t.Errorf("%s: ytmTarget(%q, %q) = %q; want %q", tt.name, tt.arg0, tt.exe, got, tt.want)
		}
	}
}
