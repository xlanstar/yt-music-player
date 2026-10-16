package session

import (
	"os/exec"
	"strings"
	"testing"
)

// TestDeps keeps what plays and signs in, which only ytmd does, out of
// the package front-ends link (docs/daemon.md, Programs).
func TestDeps(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command")
	}
	cmd := exec.CommandContext(t.Context(), gobin, "list", "-deps", ".")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	deps := map[string]bool{}
	for dep := range strings.Lines(string(out)) {
		deps[strings.TrimSpace(dep)] = true
	}
	for _, pkg := range []string{
		"github.com/egoist/mygo/ui",
		"github.com/egoist/mygo/audio",
		"yt-music-player/internal/account",
		"yt-music-player/internal/audio",
		"yt-music-player/internal/auth",
		"yt-music-player/internal/innertube",
		"yt-music-player/internal/stream",
		"yt-music-player/internal/ytmusic",
	} {
		if deps[pkg] {
			t.Errorf("session links %s", pkg)
		}
	}
}
