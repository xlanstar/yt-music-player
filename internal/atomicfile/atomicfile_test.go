package atomicfile

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// read returns path's content, failing tb if it can't.
func read(tb testing.TB, path string) string {
	tb.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		tb.Fatal(err)
	}
	return string(data)
}

// names returns the names in dir.
func names(tb testing.TB, dir string) []string {
	tb.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		tb.Fatal(err)
	}
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name()
	}
	return out
}

func TestWriteReplaces(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new")
	path := filepath.Join(dir, "state.json")
	for _, want := range []string{"first", "second"} {
		if err := WriteFile(path, []byte(want), Options{Sync: true}); err != nil {
			t.Fatal(err)
		}
		if got := read(t, path); got != want {
			t.Errorf("read %q, want %q", got, want)
		}
	}
	if got := names(t, dir); len(got) != 1 {
		t.Errorf("dir holds %q, want state.json only", got)
	}
	if runtime.GOOS == "windows" {
		return
	}
	for p, want := range map[string]fs.FileMode{dir: 0o700, path: 0o600} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s: mode %v, want %v", filepath.Base(p), got, want)
		}
	}
}

func TestWriteMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix permissions")
	}
	path := filepath.Join(t.TempDir(), "entry.plist")
	if err := WriteFile(path, []byte("x"), Options{Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Errorf("mode %v, want 0644", got)
	}
}

// A failed write leaves the old file, and no temporary one.
func TestWriteFailureKeepsTheOld(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := WriteFile(path, []byte("old"), Options{}); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	err := Write(path, Options{Sync: true}, func(w io.Writer) error {
		_, _ = io.WriteString(w, "half")
		return boom
	})
	if !errors.Is(err, boom) {
		t.Errorf("err %v, want boom", err)
	}
	if got := read(t, path); got != "old" {
		t.Errorf("read %q, want old", got)
	}
	if got := names(t, dir); len(got) != 1 {
		t.Errorf("dir holds %q, want state.json only", got)
	}
}

// A rename that fails leaves what was there, and no temporary file.
func TestWriteRenameFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	// A non-empty directory, which no file replaces.
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	leave(t, path, "kept")
	if err := WriteFile(path, []byte("new"), Options{}); err == nil {
		t.Fatal("replaced a directory")
	}
	if got := names(t, path); !slices.Equal(got, []string{"kept"}) {
		t.Errorf("the directory holds %q, want kept", got)
	}
	if got := names(t, dir); !slices.Equal(got, []string{"state.json"}) {
		t.Errorf("dir holds %q, want state.json only", got)
	}
}

// A directory that cannot be made fails the write before any file.
func TestWriteMkdirFails(t *testing.T) {
	dir := t.TempDir()
	leave(t, dir, "parent")
	if err := WriteFile(filepath.Join(dir, "parent", "state.json"), []byte("new"), Options{}); err == nil {
		t.Fatal("wrote under a file")
	}
	if got := names(t, dir); !slices.Equal(got, []string{"parent"}) {
		t.Errorf("dir holds %q, want parent only", got)
	}
}

// IsTemp tells a write in progress, as a crash would leave it.
func TestIsTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cover")
	var temps []string
	err := Write(path, Options{}, func(io.Writer) error {
		for _, name := range names(t, dir) {
			if IsTemp(name) {
				temps = append(temps, name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(temps) != 1 {
		t.Errorf("temporary files %q, want one", temps)
	}
	for _, name := range []string{"cover", ".DS_Store", ".localized", ".cover", ".cover-", ".-123", ".cover-12a", "cover-123"} {
		if IsTemp(name) {
			t.Errorf("IsTemp(%q) = true", name)
		}
	}
	if !IsTemp(".account-v2.json-4294967295") {
		t.Error("IsTemp of a name with a dash = false")
	}
}

// leave makes the files names in dir, as a crash may leave them.
func leave(tb testing.TB, dir string, names ...string) {
	tb.Helper()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			tb.Fatal(err)
		}
	}
}
