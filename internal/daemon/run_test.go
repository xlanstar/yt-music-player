//go:build !audiolive || !darwin

package daemon

import (
	"os"
	"testing"
	"yt-music-player/internal/leakcheck"
)

func runTests(m *testing.M) { os.Exit(leakcheck.Run(m)) }
