package account

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"yt-music-player/internal/catalog"
)

// sinkString keeps the compiler from optimizing measured calls away.
var sinkString string

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

// fetchCases are each Part from the placeholder, and Liked music of 2000
// songs without their IDs. Allocs today are mostly the source's.
var fetchCases = []struct {
	name  string
	src   func() catalog.Source
	part  Part
	limit float64
}{
	{"profile", placeholder, Profile, 9},  // 7 allocs today.
	{"library", placeholder, Library, 21}, // 18 allocs today.
	{"liked", placeholder, Liked, 11},     // 9 allocs today.
	{"liked/2000", bigLikes, Liked, 10},   // 8 allocs today.
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
	a.Like(id, on, func(error) {})
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

// cookieHeader is a pasted Cookie header the size of YouTube Music's,
// over several lines.
var cookieHeader = func() string {
	var sb strings.Builder
	sb.WriteString("Cookie: ")
	for i := range 40 {
		sb.WriteString("NAME" + strconv.Itoa(i) + "=" + strings.Repeat("v", 40) + ";")
		if i%8 == 7 {
			sb.WriteString("\n ")
		} else {
			sb.WriteString(" ")
		}
	}
	return sb.String()
}()

func BenchmarkCleanCredentials(b *testing.B) {
	b.SetBytes(int64(len(cookieHeader)))
	b.ReportAllocs()
	for b.Loop() {
		sinkString = CleanCredentials(cookieHeader)
	}
}
