// Package atomicfile replaces files whole: it writes a temporary file beside
// one and renames it over it, so readers and a crash see the old file or the
// new, never half of one.
package atomicfile

import (
	"cmp"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Options are how Write makes the file.
type Options struct {
	// Mode is the file's permissions; zero is 0600, only the user's.
	Mode fs.FileMode
	// Sync flushes the file to disk before the rename, for a file a crash
	// must not lose.
	Sync bool
}

// Write replaces path with what write writes, making its directory (0700)
// if missing. On failure path stays as it was, and so does its directory
// but for a missing one made.
func Write(path string, opts Options, write func(io.Writer) error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	err = write(tmp)
	if err == nil {
		// Explicit even for CreateTemp's 0600, which a filesystem may not
		// honor.
		err = tmp.Chmod(cmp.Or(opts.Mode, 0o600))
	}
	if err == nil && opts.Sync {
		err = tmp.Sync()
	}
	err = cmp.Or(err, tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}

// WriteFile replaces path with data, as Write does.
func WriteFile(path string, data []byte, opts Options) error {
	return Write(path, opts, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// IsTemp reports whether name, a base name, is one of Write's temporary
// files, which a crash may leave behind: "." + the file's name + "-" +
// CreateTemp's decimal random part.
func IsTemp(name string) bool {
	_, ok := tempOf(name)
	return ok
}

// tempOf returns the base name of the file whose temporary file name is.
func tempOf(name string) (string, bool) {
	dash := strings.LastIndexByte(name, '-')
	if !strings.HasPrefix(name, ".") || dash < 2 || dash == len(name)-1 {
		return "", false
	}
	for _, c := range []byte(name[dash+1:]) {
		if c < '0' || c > '9' {
			return "", false
		}
	}
	return name[1:dash], true
}
