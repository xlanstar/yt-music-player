//go:build darwin || linux

package autostart

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// commandTimeout bounds launchctl and systemctl, which can hang when the
// user's session manager is wedged.
const commandTimeout = 10 * time.Second

func execRun(name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput() //nolint:gosec // G204: argv built here, not from input
	if err != nil {
		return fmt.Errorf("autostart: %s %s: %w: %s", name, strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return nil
}
