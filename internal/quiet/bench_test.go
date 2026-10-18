package quiet

import "testing"

// started is a GC whose timer runs, stopped when the test ends.
func started(tb testing.TB) *GC {
	g := new(GC)
	g.Start()
	tb.Cleanup(g.Stop)
	return g
}

// BenchmarkNote is the Desktop's every build and ytmd's every batch.
func BenchmarkNote(b *testing.B) {
	g := started(b)
	b.ReportAllocs()
	for b.Loop() {
		g.Note()
	}
}
