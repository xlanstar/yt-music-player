package account

import (
	"context"
	"strconv"
	"testing"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/catalog/catalogtest"
)

// likedSource answers Liked music with songs alone, so the account makes
// the IDs.
type likedSource struct {
	catalog.Source
	songs []catalog.Song
}

func (s likedSource) Playlist(context.Context, string) (catalog.Playlist, []catalog.Song, error) {
	return catalog.Playlist{ID: catalog.LikedID}, s.songs, nil
}

// placeholderAccount is an Account over src that answers at once, so only
// the account's cost is measured; its changes go nowhere.
func placeholderAccount(src catalog.Source) (*Account, posts) {
	q := make(posts, 16)
	return New(src, func(fn func()) { q <- fn }, func(Change) {}), q
}

func bigLikes() catalog.Source {
	songs := make([]catalog.Song, 2000)
	for i := range songs {
		songs[i] = catalog.Song{ID: "liked-" + strconv.Itoa(i)}
	}
	return likedSource{catalog.NewPlaceholderSource(0), songs}
}

// decodedLikes answers Liked music as a Source decoding YouTube's pages
// does (catalogtest.Decoded).
type decodedLikes struct {
	catalog.Source
	n int
}

func (s decodedLikes) Playlist(context.Context, string) (catalog.Playlist, []catalog.Song, error) {
	return catalog.Playlist{ID: catalog.LikedID}, catalogtest.Decoded(s.n), nil
}

func decoded5000() catalog.Source { return decodedLikes{catalog.NewPlaceholderSource(0), 5000} }

// fetchCases are each Part from the placeholder, Liked music of 2000
// songs without their IDs, and 5000 described as YouTube does. Allocs
// today are mostly the source's.
var fetchCases = []struct {
	name  string
	src   func() catalog.Source
	part  Part
	limit float64
}{
	{"profile", placeholder, Profile, 9}, // 7 allocs today.
	// 25 allocs today, 7 sharing the songs' strings (shareStrings), which
	// ytmd keeps.
	{"library", placeholder, Library, 29},
	// Liked: 16 and 10 allocs today: a copy of the IDs the account keeps
	// (Latest), the songs' new slice, and 5 sharing the strings of songs
	// described.
	{"liked", placeholder, Liked, 19},
	{"liked/2000", bigLikes, Liked, 12},
	// 56 983 allocs, 3.3 MB today, the source's but 20 and 1 MB:
	// shareStrings' map and new slice, the copy of the IDs.
	{"liked/5000-described", decoded5000, Liked, 65531},
}

func placeholder() catalog.Source { return catalog.NewPlaceholderSource(0) }

// fetchRoundTrip is a fetch and its result.
func fetchRoundTrip(a *Account, q posts, p Part) {
	a.Fetch(p)
	(<-q)()
}

func BenchmarkFetch(b *testing.B) {
	for _, tc := range fetchCases {
		b.Run(tc.name, func(b *testing.B) {
			a, q := placeholderAccount(tc.src())
			b.ReportAllocs()
			for b.Loop() {
				fetchRoundTrip(a, q, tc.part)
			}
		})
	}
}

// likeRoundTrip is a Like and its answer, which goes through.
func likeRoundTrip(a *Account, q posts, id string, on bool) {
	a.Like(id, on, nil, func(error) {})
	(<-q)()
}

func BenchmarkLike(b *testing.B) {
	a, q := placeholderAccount(placeholder())
	id := catalog.Placeholder().Songs[0].ID
	on := true
	b.ReportAllocs()
	for b.Loop() {
		likeRoundTrip(a, q, id, on)
		on = !on
	}
}

// Sinks keep the compiler from optimizing measured calls away.
var (
	sinkLatest Latest
	sinkStatus Status
	sinkBool   bool
)

// bigLatest is an Account that fetched 2000 liked songs.
func bigLatest(tb testing.TB) *Account {
	tb.Helper()
	a, q := placeholderAccount(bigLikes())
	fetchRoundTrip(a, q, Liked)
	if !a.hasLikes {
		tb.Fatal("no likes")
	}
	return a
}

// BenchmarkLatest is what a front-end connecting to the daemon costs it.
func BenchmarkLatest(b *testing.B) {
	a := bigLatest(b)
	b.ReportAllocs()
	for b.Loop() {
		sinkLatest = a.Latest()
	}
}

// BenchmarkStatus is what `ytm status` costs the daemon's account.
func BenchmarkStatus(b *testing.B) {
	a := bigLatest(b)
	b.ReportAllocs()
	for b.Loop() {
		sinkStatus, sinkBool = a.Status()
	}
}

// BenchmarkKeepLike is a like that went through, kept among 2000 likes.
func BenchmarkKeepLike(b *testing.B) {
	a := bigLatest(b)
	added := Change{Likes: &Likes{Added: []string{"liked-1000"}}}
	removed := Change{Likes: &Likes{Removed: []string{"liked-1000"}}}
	b.ReportAllocs()
	for b.Loop() {
		a.keep(added)
		a.keep(removed)
	}
}
