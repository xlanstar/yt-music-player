package loader

import (
	"os"
	"testing"
	"yt-music-player/internal/leakcheck"

	"github.com/rs/zerolog"
)

func TestMain(m *testing.M) {
	// Only main sets the level; otherwise tests print every failure they
	// provoke.
	zerolog.SetGlobalLevel(zerolog.Disabled)
	os.Exit(leakcheck.Run(m))
}
