//go:build !(audiolive && darwin)

package audio

import (
	"os"
	"testing"
)

func runTests(m *testing.M) { os.Exit(m.Run()) }
