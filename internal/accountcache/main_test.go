package accountcache

import (
	"os"
	"testing"
	"yt-music-player/internal/leakcheck"
)

func TestMain(m *testing.M) { os.Exit(leakcheck.Run(m)) }
