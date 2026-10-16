package ipc

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestLockIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ytmd.lock")
	first, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("second TryLock: err %v, want ErrLocked", err)
	}
	if err := first.Unlock(); err != nil {
		t.Fatal(err)
	}
	again, err := TryLock(path)
	if err != nil {
		t.Fatalf("TryLock after Unlock: %v", err)
	}
	if err := again.Unlock(); err != nil {
		t.Fatal(err)
	}
}

func TestLockNeedsItsDirectory(t *testing.T) {
	if _, err := TryLock(filepath.Join(t.TempDir(), "missing", "ytmd.lock")); err == nil || errors.Is(err, ErrLocked) {
		t.Errorf("err %v, want the open's", err)
	}
}
