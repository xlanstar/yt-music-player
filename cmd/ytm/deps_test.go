package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// forbidden are the packages ytm must not link (docs/daemon.md, Programs):
// the window, MyGo's App and audio, everything that plays or signs in,
// which only ytmd does, and the decoders artwork leaves out of it.
var forbidden = []string{
	"github.com/egoist/mygo",
	"github.com/egoist/mygo/ui",
	"github.com/egoist/mygo/audio",
	"yt-music-player/internal/audio",
	"yt-music-player/internal/auth",
	"yt-music-player/internal/account",
	"yt-music-player/internal/innertube",
	"yt-music-player/internal/ytmusic",
	"golang.org/x/image/webp",
	"golang.org/x/image/draw",
}

// TestDeps checks what the ytm binary links, not its tests, on every OS
// it ships for.
func TestDeps(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command")
	}
	for _, goos := range []string{"darwin", "linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), gobin, "list", "-deps", "-f", "{{.ImportPath}}", ".")
			cmd.Env = append(os.Environ(), "GOOS="+goos)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("go list: %v", err)
			}
			deps := map[string]bool{}
			for dep := range strings.Lines(string(out)) {
				deps[strings.TrimSpace(dep)] = true
			}
			for _, pkg := range forbidden {
				if deps[pkg] {
					t.Errorf("ytm links %s", pkg)
				}
			}
		})
	}
}
