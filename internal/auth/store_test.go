package auth

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func testCreds(t *testing.T) Credentials {
	t.Helper()
	c, err := Parse("X-Goog-AuthUser: 1\nCookie: " + fakeCookie)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// testStore checks the Store contract.
func testStore(t *testing.T, s Store) {
	t.Helper()
	if err := s.Clear(); err != nil {
		t.Fatalf("clear empty: %v", err)
	}
	if _, err := s.Load(); !errors.Is(err, ErrNotStored) {
		t.Fatalf("load empty: err = %v, want ErrNotStored", err)
	}
	c := testCreds(t).WithNewID()
	if err := s.Save(c); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Load(); err != nil || got != c {
		t.Fatalf("load = %v, %v", got, err)
	}
	other, _ := Parse("SAPISID=other")
	if err := s.Save(other); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Load(); err != nil || got != other {
		t.Fatalf("load after replace = %v, %v", got, err)
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); !errors.Is(err, ErrNotStored) {
		t.Fatalf("load cleared: err = %v, want ErrNotStored", err)
	}
}

func TestMemoryStore(t *testing.T) { testStore(t, &MemoryStore{}) }

func TestFileStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", appName, "credentials")
	s := &fileStore{path: path}
	testStore(t, s)

	if err := s.Save(testCreds(t)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("file mode = %v, want 0600", perm)
		}
		dir, _ := os.Stat(filepath.Dir(path))
		if perm := dir.Mode().Perm(); perm != 0o700 {
			t.Errorf("dir mode = %v, want 0700", perm)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("left temporary files: %v", entries)
	}

	// Clearing removes a crash's temporary file, which holds cookies too.
	leftover := filepath.Join(filepath.Dir(path), ".credentials-123")
	if err := os.WriteFile(leftover, []byte("SID=x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 0 {
		t.Errorf("cleared, left %v", entries)
	}
}

func TestFileStoreCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials")
	if err := os.WriteFile(path, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (&fileStore{path: path}).Load(); !errors.Is(err, ErrInvalid) {
		t.Errorf("err = %v, want ErrInvalid", err)
	}
}
