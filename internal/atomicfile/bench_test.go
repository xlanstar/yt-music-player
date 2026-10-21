package atomicfile

import (
	"path/filepath"
	"testing"
)

var sinkErr error

// writeCases are a settings file's size and a cache's, 1 MB.
var writeCases = []struct {
	name string
	size int
	// limit is TestBudgetWriteFile's.
	limit float64
}{
	// 13 allocs today for each: MkdirAll's, CreateTemp's name and file,
	// Rename's; none for the data.
	{"1KB", 1 << 10, 15},
	{"1MB", 1 << 20, 15},
}

func BenchmarkWriteFile(b *testing.B) {
	for _, tc := range writeCases {
		b.Run(tc.name, func(b *testing.B) {
			path := filepath.Join(b.TempDir(), "file")
			data := make([]byte, tc.size)
			b.SetBytes(int64(tc.size))
			b.ReportAllocs()
			for b.Loop() {
				sinkErr = WriteFile(path, data, Options{})
			}
		})
	}
}
