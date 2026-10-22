package playback

import (
	"os"
	"testing"
	"yt-music-player/internal/leakcheck"

	"github.com/rs/zerolog"
)

func TestMain(m *testing.M) {
	// Tests would otherwise print every failure they provoke.
	zerolog.SetGlobalLevel(zerolog.Disabled)
	os.Exit(leakcheck.Run(m))
}
