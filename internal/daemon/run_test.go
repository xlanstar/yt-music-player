//go:build !audiolive || !darwin

package daemon

import (
	"os"
	"testing"
)

func runTests(m *testing.M) { os.Exit(m.Run()) }
