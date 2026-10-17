// Package logfile writes ytmd's and ytm's logs, which have no terminal, to
// a file of bounded size: past maxSize it becomes <name>.1.log, replacing
// the one before. The file opens at the first write, so a run that logs
// nothing leaves no file, and a failed open is tried again at the next.
//
// One process writes a file: a second one (two ytm at once) may rotate it
// under the first, losing lines at worst, never mixing them.
package logfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// maxSize is the most a log file holds before it rotates, unless one write
// alone is larger.
const maxSize = 1 << 20

// Writer is an io.Writer of whole lines for zerolog, safe for concurrent
// use: each Write lands in one file, uninterleaved.
type Writer struct {
	path, prev string
	limit      int64

	mu       sync.Mutex
	f        *os.File
	size     int64
	isClosed bool
}

// New returns a Writer that appends to path, <name>.log, rotating it to
// <name>.1.log. It creates nothing until the first write.
func New(path string) *Writer {
	return &Writer{
		path:  path,
		prev:  strings.TrimSuffix(path, ".log") + ".1.log",
		limit: maxSize,
	}
}

// Write appends p, first rotating the file if p would take it past
// maxSize.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.isClosed {
		return 0, os.ErrClosed
	}
	if w.f != nil && w.size > 0 && w.size+int64(len(p)) > w.limit {
		w.rotate()
	}
	if w.f == nil {
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// Close closes the file; later writes fail with os.ErrClosed.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.isClosed = true
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

func (w *Writer) open() error {
	if err := os.MkdirAll(filepath.Dir(w.path), 0o700); err != nil {
		return fmt.Errorf("logfile: %w", err)
	}
	f, err := os.OpenFile(w.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("logfile: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		return errors.Join(fmt.Errorf("logfile: %w", err), f.Close())
	}
	w.f, w.size = f, info.Size()
	return nil
}

// rotate moves the full file aside. Windows cannot rename a file another
// process holds open; the file then keeps growing until a rename succeeds,
// rather than losing lines.
func (w *Writer) rotate() {
	// Closed first: Windows renames no file this process holds open either.
	// A failed close loses nothing written, since writes are unbuffered.
	_ = w.f.Close()
	w.f = nil
	_ = os.Rename(w.path, w.prev)
}
