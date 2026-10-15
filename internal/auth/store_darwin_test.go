//go:build darwin

package auth

import (
	"os"
	"testing"
)

// TestKeychainStore writes to the real login Keychain (under its own
// service), so it runs only with YTM_KEYCHAIN_TEST=1. macOS may ask once
// to allow the test binary.
func TestKeychainStore(t *testing.T) {
	if os.Getenv("YTM_KEYCHAIN_TEST") != "1" {
		t.Skip("touches the login Keychain; set YTM_KEYCHAIN_TEST=1")
	}
	if err := loadSecurity(); err != nil {
		t.Fatal(err)
	}
	s := &keychainStore{service: appName + "-test", account: "test"}
	t.Cleanup(func() { _ = s.Clear() })
	testStore(t, s)
}
