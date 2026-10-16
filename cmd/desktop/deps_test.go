package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// forbidden are what the Desktop must not link (docs/daemon.md, Programs):
// everything that plays or signs in, which only ytmd does.
var forbidden = []string{
	"yt-music-player/internal/audio",
	"yt-music-player/internal/auth",
	"yt-music-player/internal/account",
	"yt-music-player/internal/stream",
	"yt-music-player/internal/innertube",
	"yt-music-player/internal/ytmusic",
}

// TestDeps checks what the Desktop binary links, not its tests, which run
// a daemon in process, on every OS it ships for, the placeholder build
// too.
func TestDeps(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command")
	}
	for _, goos := range []string{"darwin", "linux", "windows"} {
		for _, tags := range []string{"", "placeholder"} {
			t.Run(goos+"/"+tags, func(t *testing.T) {
				t.Parallel()
				cmd := exec.CommandContext(t.Context(), gobin, "list", "-deps", "-tags", tags, "-f", "{{.ImportPath}}", ".")
				cmd.Env = append(os.Environ(), "GOOS="+goos)
				out, err := cmd.Output()
				if err != nil {
					t.Fatalf("go list: %v", err)
				}
				deps := map[string]bool{}
				for dep := range strings.Lines(string(out)) {
					deps[strings.TrimSpace(dep)] = true
				}
				if !deps["yt-music-player/internal/remote"] {
					t.Fatal("go list listed no remote: wrong package?")
				}
				for _, pkg := range forbidden {
					if deps[pkg] {
						t.Errorf("the Desktop links %s", pkg)
					}
				}
			})
		}
	}
}
