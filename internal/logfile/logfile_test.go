package logfile

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// line is a log line of n bytes, newline included.
func line(n int) []byte {
	return append(bytes.Repeat([]byte{'x'}, n-1), '\n')
}

func write(t *testing.T, w *Writer, p []byte) {
	t.Helper()
	n, err := w.Write(p)
	if err != nil || n != len(p) {
		t.Fatalf("Write = %d, %v; want %d, nil", n, err, len(p))
	}
}

func size(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

func closeWriter(t *testing.T, w *Writer) {
	t.Helper()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRotatesAtMaxSize(t *testing.T) {
	dir := t.TempDir()
	w := New(dir, "ytmd")
	defer closeWriter(t, w)
	cur, prev := filepath.Join(dir, "ytmd.log"), filepath.Join(dir, "ytmd.1.log")

	// Filling the file exactly keeps it.
	for range MaxSize / 1024 {
		write(t, w, line(1024))
	}
	if got := size(t, cur); got != MaxSize {
		t.Fatalf("log holds %d bytes, want %d", got, MaxSize)
	}
	if _, err := os.Stat(prev); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rotated before the file was full: %v", err)
	}

	// One byte more starts a new file.
	write(t, w, line(10))
	if got := size(t, prev); got != MaxSize {
		t.Errorf("%s holds %d bytes, want %d", prev, got, MaxSize)
	}
	if got := size(t, cur); got != 10 {
		t.Errorf("%s holds %d bytes, want 10", cur, got)
	}
}

func TestKeepsTwoFiles(t *testing.T) {
	dir := t.TempDir()
	w := New(dir, "ytm")
	w.limit = 100
	defer closeWriter(t, w)
	for i := range 5 {
		write(t, w, []byte(strconv.Itoa(i)+strings.Repeat(".", 98)+"\n"))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("%d files, want 2: %v", len(entries), entries)
	}
	for name, want := range map[string]byte{"ytm.log": '4', "ytm.1.log": '3'} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if got[0] != want {
			t.Errorf("%s starts %q, want %q", name, got[0], want)
		}
	}
}

func TestLargeWriteFillsAFileAlone(t *testing.T) {
	dir := t.TempDir()
	w := New(dir, "ytmd")
	w.limit = 100
	defer closeWriter(t, w)
	write(t, w, line(10))
	write(t, w, line(300))
	write(t, w, line(10))
	if got := size(t, filepath.Join(dir, "ytmd.1.log")); got != 300 {
		t.Errorf("the large line's file holds %d bytes, want 300", got)
	}
	if got := size(t, filepath.Join(dir, "ytmd.log")); got != 10 {
		t.Errorf("the next file holds %d bytes, want 10", got)
	}
}

func TestReopenAppends(t *testing.T) {
	dir := t.TempDir()
	w := New(dir, "ytmd")
	write(t, w, []byte("first\n"))
	closeWriter(t, w)

	w = New(dir, "ytmd")
	w.limit = 20
	write(t, w, []byte("second\n"))
	got, err := os.ReadFile(filepath.Join(dir, "ytmd.log"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "first\nsecond\n" {
		t.Errorf("log = %q, want both runs' lines", got)
	}

	// The earlier run's bytes count toward the limit.
	write(t, w, []byte("third line\n"))
	closeWriter(t, w)
	if got := size(t, filepath.Join(dir, "ytmd.1.log")); got != 13 {
		t.Errorf("rotated file holds %d bytes, want 13", got)
	}
}

func TestCreatesPrivately(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows keeps no Unix modes")
	}
	dir := filepath.Join(t.TempDir(), "cache", "logs")
	w := New(dir, "ytmd")
	defer closeWriter(t, w)
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("New made the directory before any write: %v", err)
	}
	write(t, w, line(5))
	for path, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, "ytmd.log"): 0o600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s has mode %v, want %v", path, got, want)
		}
	}
}

func TestRetriesAFailedOpen(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "logs")
	if err := os.WriteFile(dir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	w := New(dir, "ytmd")
	defer closeWriter(t, w)
	if _, err := w.Write(line(5)); err == nil {
		t.Fatal("Write succeeded where its directory is a file")
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	write(t, w, line(5))
}

func TestWriteAfterClose(t *testing.T) {
	w := New(t.TempDir(), "ytmd")
	write(t, w, line(5))
	closeWriter(t, w)
	if _, err := w.Write(line(5)); !errors.Is(err, os.ErrClosed) {
		t.Errorf("Write after Close = %v, want os.ErrClosed", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("second Close = %v", err)
	}
}

func TestConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	w := New(dir, "ytmd")
	w.limit = 4096
	const writers, lines = 8, 200
	var wg sync.WaitGroup
	for g := range writers {
		wg.Go(func() {
			p := []byte("writer " + strconv.Itoa(g) + " " + strings.Repeat("-", 40) + "\n")
			for range lines {
				if _, err := w.Write(p); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	closeWriter(t, w)

	// Rotation keeps the latest two files, so count what they hold: every
	// line whole, none split across another.
	var whole int
	for _, name := range []string{"ytmd.1.log", "ytmd.log"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		for l := range strings.Lines(string(data)) {
			if !strings.HasPrefix(l, "writer ") || !strings.HasSuffix(l, strings.Repeat("-", 40)+"\n") {
				t.Fatalf("%s has a torn line %q", name, l)
			}
			whole++
		}
	}
	if whole == 0 {
		t.Error("no lines written")
	}
}
