package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// forbidden are what ytmd must not link (docs/daemon.md, Programs): the UI,
// MyGo's App and its languages, and the front-ends' state; profiling only
// in a memprof build.
var forbidden = []string{
	"net/http/pprof",
	"runtime/pprof",
	"github.com/egoist/mygo",
	"github.com/egoist/mygo/ui",
	"github.com/egoist/mygo/i18n",
	"yt-music-player/locales",
	"yt-music-player/internal/session",
	"yt-music-player/internal/loader",
}

func TestDeps(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command")
	}
	for _, goos := range []string{"darwin", "linux", "windows"} {
		for _, tags := range []string{"", "placeholder"} {
			t.Run(goos+"/"+tags, func(t *testing.T) {
				t.Parallel()
				cmd := exec.CommandContext(t.Context(), gobin, "list", "-deps", "-tags", tags, ".")
				cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH=amd64")
				out, err := cmd.Output()
				if err != nil {
					t.Fatalf("go list: %v", err)
				}
				deps := map[string]bool{}
				for dep := range strings.Lines(string(out)) {
					deps[strings.TrimSpace(dep)] = true
				}
				if !deps["yt-music-player/internal/daemon"] {
					t.Fatal("go list listed no daemon: wrong package?")
				}
				for _, pkg := range forbidden {
					if deps[pkg] {
						t.Errorf("ytmd links %s", pkg)
					}
				}
			})
		}
	}
}
