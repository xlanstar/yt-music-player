package session

import (
	"strconv"
	"strings"
	"testing"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/fetchstate"
	"yt-music-player/internal/loader"
	"yt-music-player/internal/notice"
)

// Sinks keep the compiler from optimizing measured calls away.
var (
	sinkBool    bool
	sinkInt     int
	sinkString  string
	sinkSongs   []catalog.Song
	sinkNotices []notice.Notice
)

const bigLikes = 2000

// idleSession is a Session as a build finds it: signed in to the
// placeholder (which answers at once), library fetched, and likeCount
// songs liked, all in the catalog.
func idleSession(tb testing.TB, likeCount int) (*Session, posts) {
	tb.Helper()
	cat, q := catalog.Placeholder(), make(posts, 16)
	post := func(fn func()) { q <- fn }
	src := catalog.NewPlaceholderSource(0)
	var states fetchstate.Registry
	s := New(src, &states, loader.New(src, &states, cat, post), cat, post)
	s.Start()
	for range Resources {
		(<-q)()
	}
	if !s.IsSignedIn() || s.IsBusy() {
		tb.Fatal("not signed in")
	}
	songs := make([]catalog.Song, likeCount)
	ids := make([]string, likeCount)
	for i := range songs {
		ids[i] = "liked-" + strconv.Itoa(i)
		songs[i] = catalog.Song{ID: ids[i], Title: "Liked " + strconv.Itoa(i), Artist: "Artist", Seconds: 180}
	}
	cat.Add(songs, nil, nil)
	s.KeepLikes(ids)
	return s, q
}

// BenchmarkIdle measures what every build asks of the session.
func BenchmarkIdle(b *testing.B) {
	s, _ := idleSession(b, bigLikes)
	for _, tc := range idleCases(s) {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				tc.call()
			}
		})
	}
}

type idleCase struct {
	name string
	call func()
}

func idleCases(s *Session) []idleCase {
	return []idleCase{
		{"Start", s.Start},
		{"TakeNotices", func() { sinkNotices = s.TakeNotices() }},
		{"CancelSignIn", s.CancelSignIn},
		{"SignedIn", func() { sinkBool = s.IsSignedIn() }},
		{"DisplayName", func() { sinkString = s.DisplayName() }},
		{"CanSignIn", func() { sinkBool = s.CanSignIn() }},
		{"Busy", func() { sinkBool = s.IsBusy() }},
		{"LikesGen", func() { sinkInt = int(s.LikesGen()) }},
		{"Liked", func() { sinkBool = s.Liked("liked-1234") }},
		{"NotLiked", func() { sinkBool = s.Liked("nope") }},
	}
}

// BenchmarkLikedCount: Liked music's card asks it once the likes change.
func BenchmarkLikedCount(b *testing.B) {
	s, _ := idleSession(b, bigLikes)
	b.ReportAllocs()
	for b.Loop() {
		sinkInt = s.LikedCount()
	}
}

// BenchmarkLikedSongs: Liked music's page asks it once the likes or the
// catalog change.
func BenchmarkLikedSongs(b *testing.B) {
	s, _ := idleSession(b, bigLikes)
	b.ReportAllocs()
	for b.Loop() {
		sinkSongs = s.LikedSongs()
	}
}

func BenchmarkApplyLike(b *testing.B) {
	s, _ := idleSession(b, bigLikes)
	b.ReportAllocs()
	on := false
	for b.Loop() {
		s.applyLike("liked-1234", on)
		on = !on
	}
}

func BenchmarkSeedLikes(b *testing.B) {
	s, _ := idleSession(b, bigLikes)
	ids := s.likeOrder
	b.ReportAllocs()
	for b.Loop() {
		s.seedLikes(ids)
	}
}

// likeRoundTrip is a Like click and its answer from a source that answers
// at once, so only the session's cost is measured.
func likeRoundTrip(s *Session, q posts, id string, on bool) {
	s.SetLiked(id, on)
	(<-q)()
}

func BenchmarkSetLiked(b *testing.B) {
	s, q := idleSession(b, bigLikes)
	id := catalog.Placeholder().Songs[0].ID
	on := !s.Liked(id)
	b.ReportAllocs()
	for b.Loop() {
		likeRoundTrip(s, q, id, on)
		on = !on
	}
}

// BenchmarkRetry measures the job, the placeholder and the seeding, from
// a source that answers at once.
func BenchmarkRetry(b *testing.B) {
	s, q := idleSession(b, 0)
	b.ReportAllocs()
	for b.Loop() {
		s.Retry(LikedRes)
		(<-q)()
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
