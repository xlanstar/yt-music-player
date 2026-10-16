package audio

import (
	"testing"

	"github.com/rs/zerolog"
)

func TestMain(m *testing.M) {
	// Tests would otherwise print every failure they provoke.
	zerolog.SetGlobalLevel(zerolog.Disabled)
	runTests(m)
}
