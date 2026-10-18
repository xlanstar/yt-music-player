package quiet

import "testing"

var sinkBytes []byte

func TestFree(t *testing.T) {
	var g GC
	// Never started: nothing to reset.
	g.Note()
	sinkBytes = make([]byte, 2*minAllocated)
	g.free()
	first := g.last.Load()
	if first == 0 {
		t.Fatal("did not free after 2 MB")
	}
	// A quiet stretch that allocated next to nothing (a song playing
	// unfocused) frees nothing.
	g.free()
	if g.last.Load() != first {
		t.Fatal("freed with nothing allocated")
	}
	sinkBytes = make([]byte, 2*minAllocated)
	g.free()
	if g.last.Load() == first {
		t.Fatal("did not free after 2 MB more")
	}
}
