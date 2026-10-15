package loader

import (
	"os"
	"testing"

	"github.com/rs/zerolog"
)

func TestMain(m *testing.M) {
	// Only main sets the level; otherwise tests print every failure they
	// provoke.
	zerolog.SetGlobalLevel(zerolog.Disabled)
	os.Exit(m.Run())
}
