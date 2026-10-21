package session

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/catalog/catalogtest"
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

func (instantDaemon) Refresh(done func(error)) { done(nil) }
func (instantDaemon) Like(_ string, _ bool, _ *catalog.Song, done func(error)) {
	done(nil)
}

func (instantDaemon) SignIn(_ string, done func(catalog.Account, error)) {
	done(catalog.Account{}, nil)
}
func (instantDaemon) CancelSignIn()            {}
func (instantDaemon) SignOut(done func(error)) { done(nil) }

// newSession is a Session over the placeholder's source, which answers
// at once, and cat; its fetches' results queue in q.
func newSession(cat *catalog.Catalog) (*Session, chan func()) {
	return sessionOver(catalog.NewPlaceholderSource(0), cat)
}

func sessionOver(src catalog.Source, cat *catalog.Catalog) (*Session, chan func()) {
	q := make(chan func(), 4)
	var fetches fetchstate.Registry
	s := New(Options{
		Daemon: instantDaemon{}, Source: src, Post: func(fn func()) { q <- fn },
		Fetches: &fetches, Loader: loader.New(src, &fetches, cat, func(func()) {}), Catalog: cat,
	})
	return s, q
}

// decodedLikes answers Liked music as the remote Client decodes it
// (catalogtest.Decoded), every string a copy of its own, the IDs too.
type decodedLikes struct {
	catalog.Source
	n int
}

func (s decodedLikes) Playlist(context.Context, string) (catalog.Playlist, []catalog.Song, error) {
	songs := catalogtest.Decoded(s.n)
	ids := make([]string, len(songs))
	for i, song := range songs {
		ids[i] = strings.Clone(song.ID)
	}
	return catalog.Playlist{ID: catalog.LikedID, Songs: ids}, songs, nil
}

// idleSession is a Session as a build finds it: signed in to the
// placeholder, library fetched, and bigLikes songs liked, all in the
// catalog.
func idleSession(tb testing.TB) *Session {
	tb.Helper()
	cat, src := catalog.Placeholder(), catalog.NewPlaceholderSource(0)
	s, _ := newSession(cat)
	user, err := src.Account(tb.Context())
	if err != nil {
		tb.Fatal(err)
	}
	lib, err := src.Library(tb.Context())
	if err != nil {
		tb.Fatal(err)
	}
	s.KeepAccount(user, lib)
	songs := make([]catalog.Song, bigLikes)
	ids := make([]string, bigLikes)
	for i := range songs {
		ids[i] = "liked-" + strconv.Itoa(i)
		songs[i] = catalog.Song{ID: ids[i], Title: "Liked " + strconv.Itoa(i), Artist: "Artist", Seconds: 180}
	}
	cat.Add(songs, nil, nil)
	s.KeepLikes(ids)
	if !s.IsSignedIn() || s.IsBusy() {
		tb.Fatal("not signed in")
	}
	return s
}

// BenchmarkIdle measures what every build asks of the session.
func BenchmarkIdle(b *testing.B) {
	s := idleSession(b)
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
		{"Liked", func() { sinkBool = s.IsLiked("liked-1234") }},
		{"NotLiked", func() { sinkBool = s.IsLiked("nope") }},
	}
}

// BenchmarkLikedCount: Liked music's card asks it once the likes change.
func BenchmarkLikedCount(b *testing.B) {
	s := idleSession(b)
	b.ReportAllocs()
	for b.Loop() {
		sinkInt = s.LikedCount()
	}
}

// BenchmarkLikedSongs: Liked music's page asks it once the likes or the
// catalog change.
func BenchmarkLikedSongs(b *testing.B) {
	s := idleSession(b)
	b.ReportAllocs()
	for b.Loop() {
		sinkSongs = s.LikedSongs()
	}
}

func BenchmarkApplyLike(b *testing.B) {
	s := idleSession(b)
	b.ReportAllocs()
	on := false
	for b.Loop() {
		s.applyLike("liked-1234", on)
		on = !on
	}
}

func BenchmarkSeedLikes(b *testing.B) {
	s := idleSession(b)
	ids := s.likeOrder
	b.ReportAllocs()
	for b.Loop() {
		s.seedLikes(ids)
	}
}

// BenchmarkSetLiked measures a Like click and its answer, from a daemon
// that answers at once.
func BenchmarkSetLiked(b *testing.B) {
	s := idleSession(b)
	id := catalog.Placeholder().Songs[0].ID
	on := !s.IsLiked(id)
	b.ReportAllocs()
	for b.Loop() {
		s.SetLiked(id, on)
		on = !on
	}
}

// retryLikes is a Retry of Liked music and its result.
func retryLikes(s *Session, q chan func()) {
	s.Retry(LikedRes)
	(<-q)()
}

// BenchmarkRetry measures fetching and seeding the placeholder's likes,
// from a source that answers at once.
func BenchmarkRetry(b *testing.B) {
	s, q := newSession(catalog.Placeholder())
	s.KeepAccount(catalog.Account{Name: "Lan"}, catalog.Library{})
	b.ReportAllocs()
	for b.Loop() {
		retryLikes(s, q)
	}
}
