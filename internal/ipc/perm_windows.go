package ipc

import (
	"fmt"
	"os"
)

// privateDir makes dir. %LOCALAPPDATA%'s ACL already keeps others out.
func privateDir(dir string, _ int, _ bool) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("ipc: %w", err)
	}
	return nil
}

// privateSocket does nothing: chmod means nothing on Windows, where the
// directory's ACL keeps others out.
func privateSocket(string) error { return nil }
