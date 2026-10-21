package ytmusic

import (
	"os"
	"testing"

	"github.com/rs/zerolog"
)

func TestMain(m *testing.M) {
	// Tests would otherwise print every failure they provoke.
	zerolog.SetGlobalLevel(zerolog.Disabled)
	os.Exit(m.Run())
}
