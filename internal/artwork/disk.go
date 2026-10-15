package artwork

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// DefaultDir returns where the app keeps fetched images between runs.
func DefaultDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "yt-music-player", "art"), nil
}

// disk keeps fetched images as the server sent them, in files named by
// their URL's hash. Reading a file touches it, so pruning drops those
// unused longest. A nil *disk keeps nothing.
type disk struct {
	dir      string
	maxBytes int64

	mu sync.Mutex
	// size is the bytes the files take, valid once scanned.
	size      int64
	isScanned bool
}

func (d *disk) path(u string) string {
	h := sha256.Sum256([]byte(u))
	return filepath.Join(d.dir, hex.EncodeToString(h[:16]))
}

func (d *disk) get(u string) ([]byte, bool) {
	if d == nil {
		return nil, false
	}
	p := d.path(u)
	data, err := os.ReadFile(p) //nolint:gosec // p is the cache dir and a hash
	if err != nil || len(data) == 0 {
		return nil, false
	}
	now := time.Now()
	_ = os.Chtimes(p, now, now)
	return data, true
}

func (d *disk) remove(u string) {
	if d != nil {
		_ = os.Remove(d.path(u))
	}
}

// put stores data and prunes once over budget. Errors are ignored: the
// image shows anyway and is fetched again next time.
func (d *disk) put(u string, data []byte) {
	if d == nil {
		return
	}
	if err := os.MkdirAll(d.dir, 0o700); err != nil {
		return
	}
	// Rename is atomic, so readers never see half a file, even after a crash.
	f, err := os.CreateTemp(d.dir, "tmp-*")
	if err != nil {
		return
	}
	_, err = f.Write(data)
	err = cmp.Or(err, f.Close())
	if err == nil {
		err = os.Rename(f.Name(), d.path(u))
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.isScanned {
		d.prune()
		return
	}
	d.size += int64(len(data))
	if d.size > d.maxBytes {
		d.prune()
	}
}

// prune removes the least recently used files down to three quarters of
// the budget, leaving room for many writes before the next prune. Needs d.mu.
func (d *disk) prune() {
	type file struct {
		path string
		size int64
		used time.Time
	}
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return
	}
	var (
		files []file
		total int64
	)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		p := filepath.Join(d.dir, e.Name())
		// A temporary file older than any write is left from a crash.
		if strings.HasPrefix(e.Name(), "tmp-") && time.Since(info.ModTime()) > time.Hour {
			_ = os.Remove(p)
			continue
		}
		files = append(files, file{path: p, size: info.Size(), used: info.ModTime()})
		total += info.Size()
	}
	d.isScanned = true
	d.size = total
	if total <= d.maxBytes {
		return
	}
	slices.SortFunc(files, func(a, b file) int { return a.used.Compare(b.used) })
	for _, f := range files {
		if d.size <= d.maxBytes*3/4 {
			break
		}
		if err := os.Remove(f.path); err == nil || errors.Is(err, fs.ErrNotExist) {
			d.size -= f.size
		}
	}
}
