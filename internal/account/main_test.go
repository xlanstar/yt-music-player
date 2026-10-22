package account

import (
	"os"
	"testing"
	"yt-music-player/internal/leakcheck"

	"github.com/rs/zerolog"
)

func TestMain(m *testing.M) {
	// Only main sets the level: tests would otherwise print every failure
	// they provoke.
	zerolog.SetGlobalLevel(zerolog.Disabled)
	os.Exit(leakcheck.Run(m))
}
