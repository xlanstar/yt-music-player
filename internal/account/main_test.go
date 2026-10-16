package account

import (
	"os"
	"testing"

	"github.com/rs/zerolog"
)

func TestMain(m *testing.M) {
	// Only main sets the level: tests would otherwise print every failure
	// they provoke.
	zerolog.SetGlobalLevel(zerolog.Disabled)
	os.Exit(m.Run())
}
