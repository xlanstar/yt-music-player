package main

import "testing"

var sinkBytes []byte

func TestQuietGC(t *testing.T) {
	var q quietGC
	// Tests' apps build with a never-started quietGC.
	q.built()
	sinkBytes = make([]byte, 2*quietMin)
	q.free()
	first := q.last.Load()
	if first == 0 {
		t.Fatal("did not free after 2 MB")
	}
	// A quiet stretch that allocated next to nothing (a song playing
	// unfocused) frees nothing.
	q.free()
	if q.last.Load() != first {
		t.Fatal("freed with nothing allocated")
	}
	sinkBytes = make([]byte, 2*quietMin)
	q.free()
	if q.last.Load() == first {
		t.Fatal("did not free after 2 MB more")
	}
}
