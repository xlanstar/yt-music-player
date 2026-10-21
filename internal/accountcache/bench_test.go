package accountcache

import (
	"os"
	"path/filepath"
	"testing"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/catalog/catalogtest"
)

var sinkSaved Saved

// likedCount is the scaled case: 5000 liked songs, YouTube's most, and
// a library of 500 songs.
const likedCount = 5000

// bigSaved is a library and likes as decoded, every string a copy.
func bigSaved() *Saved {
	liked := catalogtest.Decoded(likedCount)
	ids := make([]string, len(liked))
	for i, song := range liked {
		ids[i] = song.ID
	}
	return &Saved{
		Account:    catalog.Account{Name: "Ada Lovelace", Handle: "@ada"},
		Library:    catalog.Library{Songs: catalogtest.Decoded(500)},
		Liked:      ids,
		LikedSongs: liked,
	}
}

// savedFile is bigSaved saved in tb's TempDir; size is the file's.
func savedFile(tb testing.TB) (f File, size int64) {
	tb.Helper()
	f = File(filepath.Join(tb.TempDir(), "account.json"))
	if err := f.Save(bigSaved()); err != nil {
		tb.Fatal(err)
	}
	info, err := os.Stat(string(f))
	if err != nil {
		tb.Fatal(err)
	}
	return f, info.Size()
}

// BenchmarkLoad is a front-end's start, signed in.
func BenchmarkLoad(b *testing.B) {
	f, size := savedFile(b)
	b.SetBytes(size)
	b.ReportAllocs()
	for b.Loop() {
		s, err := f.Load()
		if err != nil {
			b.Fatal(err)
		}
		sinkSaved = s
	}
}

// BenchmarkSave is the save after each fetch of the library and likes.
func BenchmarkSave(b *testing.B) {
	f := File(filepath.Join(b.TempDir(), "account.json"))
	s := bigSaved()
	b.ReportAllocs()
	for b.Loop() {
		if err := f.Save(s); err != nil {
			b.Fatal(err)
		}
	}
}
