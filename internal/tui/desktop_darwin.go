package tui

import (
	"context"
	"os/exec"
	"time"
)

// desktopBundle is the Desktop app's bundle identifier (mygo.json).
const desktopBundle = "dev.lanstar.ytmusicplayer"

// openTimeout bounds open(1), which returns once Launch Services took the
// request.
const openTimeout = 10 * time.Second

// openDesktop opens the Desktop app by its bundle identifier, wherever it
// is installed.
func openDesktop() error {
	ctx, cancel := context.WithTimeout(context.Background(), openTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "open", "-b", desktopBundle).Run()
}
