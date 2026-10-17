package logfile

import (
	"path/filepath"
	"testing"
)

// Sinks keep the compiler from optimizing measured calls away.
var (
	sinkInt int
	sinkErr error
)

// event is a zerolog line of a typical failure at debug.
var event = []byte(`{"level":"debug","id":"dQw4w9WgXcQ","error":"stream: no opus format","time":"2026-01-02T15:04:05Z","message":"resolve failed"}` + "\n")

// BenchmarkWrite costs a line into a file that rotates now and then, as
// it does under a debug log.
func BenchmarkWrite(b *testing.B) {
	for _, tc := range []struct {
		name  string
		limit int64
	}{
		{"append", maxSize},
		{"rotate-every-64", int64(64 * len(event))},
	} {
		b.Run(tc.name, func(b *testing.B) {
			w := New(filepath.Join(b.TempDir(), "ytmd.log"))
			w.limit = tc.limit
			b.Cleanup(func() { _ = w.Close() })
			b.SetBytes(int64(len(event)))
			b.ReportAllocs()
			for b.Loop() {
				sinkInt, sinkErr = w.Write(event)
			}
		})
	}
}

func BenchmarkLevel(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_, sinkErr = Level(" Debug ")
	}
}
