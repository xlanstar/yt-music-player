//go:build !darwin

package auth

import (
	"fmt"
	"os"
	"path/filepath"
)

// NewStore returns the platform's store: a file only the user can read in
// os.UserConfigDir, the login Keychain on macOS.
func NewStore() (Store, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	return &fileStore{path: filepath.Join(dir, appName, "credentials")}, nil
}
