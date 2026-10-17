package session

import (
	"context"
	"strconv"
	"testing"
	"yt-music-player/internal/api"
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

// instantDaemon answers every request at once, so only the session's
// cost is measured.
type instantDaemon struct{}

func (instantDaemon) Refresh(_ api.Part, done func(error)) { done(nil) }
func (instantDaemon) Like(_ string, _ bool, _ *catalog.Song, done func(error)) {
	done(nil)
}

func (instantDaemon) SignIn(_ string, done func(catalog.Account, error)) {
	done(catalog.Account{}, nil)
}
func (instantDaemon) CancelSignIn()            {}
func (instantDaemon) SignOut(done func(error)) { done(nil) }

// placeholderLikes are the placeholder's liked songs, as the daemon's
// event.
func placeholderLikes(tb testing.TB) api.Likes {
	tb.Helper()
	pl, songs, err := catalog.NewPlaceholderSource(0).Playlist(context.Background(), catalog.LikedID)
	if err != nil {
		tb.Fatal(err)
	}
	return api.Likes{Full: true, IDs: pl.Songs, Songs: songs}
}

// idleSession is a Session as a build finds it: signed in to the
// placeholder, library fetched, and likeCount songs liked, all in the
// catalog.
func idleSession(tb testing.TB, likeCount int) *Session {
	tb.Helper()
	cat, src := catalog.Placeholder(), catalog.NewPlaceholderSource(0)
	var states fetchstate.Registry
	s := New(instantDaemon{}, &states, loader.New(src, &states, cat, func(func()) {}), cat)
	user, err := src.Account(context.Background())
	if err != nil {
		tb.Fatal(err)
	}
	lib, err := src.Library(context.Background())
	if err != nil {
		tb.Fatal(err)
	}
	s.ApplyAccount(api.AccountEvent{Account: user, SignedIn: true})
	s.ApplyLibrary(lib)
	s.ApplyLikes(placeholderLikes(tb))
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
	return s
}

// BenchmarkIdle measures what every build asks of the session.
func BenchmarkIdle(b *testing.B) {
	s := idleSession(b, bigLikes)
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
		{"TakeNotices", func() { sinkNotices = s.TakeNotices() }},
		{"CancelSignIn", s.CancelSignIn},
		{"SignedIn", func() { sinkBool = s.IsSignedIn() }},
		{"DisplayName", func() { sinkString = s.DisplayName() }},
		{"Busy", func() { sinkBool = s.IsBusy() }},
		{"LikesGen", func() { sinkInt = int(s.LikesGen()) }},
		{"Liked", func() { sinkBool = s.Liked("liked-1234") }},
		{"NotLiked", func() { sinkBool = s.Liked("nope") }},
	}
}

// BenchmarkLikedCount: Liked music's card asks it once the likes change.
func BenchmarkLikedCount(b *testing.B) {
	s := idleSession(b, bigLikes)
	b.ReportAllocs()
	for b.Loop() {
		sinkInt = s.LikedCount()
	}
}

// BenchmarkLikedSongs: Liked music's page asks it once the likes or the
// catalog change.
func BenchmarkLikedSongs(b *testing.B) {
	s := idleSession(b, bigLikes)
	b.ReportAllocs()
	for b.Loop() {
		sinkSongs = s.LikedSongs()
	}
}

func BenchmarkApplyLike(b *testing.B) {
	s := idleSession(b, bigLikes)
	b.ReportAllocs()
	on := false
	for b.Loop() {
		s.applyLike("liked-1234", on)
		on = !on
	}
}

func BenchmarkSeedLikes(b *testing.B) {
	s := idleSession(b, bigLikes)
	ids := s.likeOrder
	b.ReportAllocs()
	for b.Loop() {
		s.seedLikes(ids)
	}
}

// BenchmarkSetLiked measures a Like click and its answer, from a daemon
// that answers at once.
func BenchmarkSetLiked(b *testing.B) {
	s := idleSession(b, bigLikes)
	id := catalog.Placeholder().Songs[0].ID
	on := !s.Liked(id)
	b.ReportAllocs()
	for b.Loop() {
		s.SetLiked(id, on)
		on = !on
	}
}

// retryLikes is a Retry of Liked music and the event of its fetch.
func retryLikes(s *Session, likes api.Likes) {
	s.Retry(LikedRes)
	s.ApplyLikes(likes)
}

// BenchmarkRetry measures the request and the seeding of the
// placeholder's likes, from a daemon that answers at once.
func BenchmarkRetry(b *testing.B) {
	s := idleSession(b, 0)
	likes := placeholderLikes(b)
	b.ReportAllocs()
	for b.Loop() {
		retryLikes(s, likes)
	}
}
