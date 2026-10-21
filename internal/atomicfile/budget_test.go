package atomicfile

import (
	"path/filepath"
	"testing"
	"yt-music-player/internal/budget"
)

func TestBudgetWriteFile(t *testing.T) {
	for _, tc := range writeCases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "file")
			data := make([]byte, tc.size)
			budget.Allocs(t, tc.limit, 20, func() { sinkErr = WriteFile(path, data, Options{}) })
		})
	}
}
