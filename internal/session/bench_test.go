package session

import (
	"context"
	"io/fs"
	"strconv"
	"strings"
	"testing"
	"yt-music-player/internal/accountcache"
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

// BenchmarkSeedLikes seeds bigLikes likes into a Session's first map, as a
// start or a sign-in does, and again, as each refetch does.
func BenchmarkSeedLikes(b *testing.B) {
	s := idleSession(b)
	ids := s.likeOrder
	b.Run("first", func(b *testing.B) {
		benchFresh(b, newSessions, func(s *Session) { s.seedLikes(ids) })
	})
	b.Run("again", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			s.seedLikes(ids)
		}
	})
}

// newSessions makes n Sessions over the placeholder and an empty catalog.
func newSessions(n int) []*Session {
	sessions := make([]*Session, n)
	for i := range sessions {
		sessions[i], _ = newSession(catalog.New(nil, nil, nil))
	}
	return sessions
}

// freshBatch is how many Sessions benchFresh makes at a time.
const freshBatch = 16

// benchFresh measures call on Sessions used once, which newBatch makes outside
// the timer, a batch at a time.
func benchFresh(b *testing.B, newBatch func(n int) []*Session, call func(*Session)) {
	var batch []*Session
	b.ReportAllocs()
	for b.Loop() {
		if len(batch) == 0 {
			b.StopTimer()
			batch = newBatch(freshBatch)
			b.StartTimer()
		}
		call(batch[0])
		// Dropped, so that used ones do not pile up.
		batch[0], batch = nil, batch[1:]
	}
}

// fresh returns a func handing out n Sessions made by newBatch beforehand, one
// a call, so a budget counts only what is done with each.
func fresh(n int, newBatch func(n int) []*Session) func() *Session {
	batch := newBatch(n)
	return func() *Session {
		s := batch[0]
		batch[0], batch = nil, batch[1:]
		return s
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

// servedLikes answers Liked music with the same songs every time, so that
// only the Session's cost is measured.
type servedLikes struct {
	catalog.Source
	playlist catalog.Playlist
	songs    []catalog.Song
}

func newServedLikes(n int) servedLikes {
	songs := catalogtest.Decoded(n)
	ids := make([]string, len(songs))
	for i, song := range songs {
		ids[i] = song.ID
	}
	return servedLikes{catalog.NewPlaceholderSource(0), catalog.Playlist{ID: catalog.LikedID, Songs: ids}, songs}
}

func (s servedLikes) Playlist(context.Context, string) (catalog.Playlist, []catalog.Song, error) {
	return s.playlist, s.songs, nil
}

// retryCases are Retries of Liked music from a source that answers at
// once: the placeholder's, and 5000 as the remote Client decodes them.
var retryCases = []struct {
	name    string
	session func() (*Session, chan func())
	limit   float64
}{
	// 13 allocs today: the job's context, goroutine and post, the source's
	// copies, the maps of ShareStrings.
	{"placeholder", func() (*Session, chan func()) { return newSession(catalog.Placeholder()) }, 15},
	// 24 allocs today, most of them the map of ShareStrings.
	{"5000", func() (*Session, chan func()) {
		return sessionOver(newServedLikes(5000), catalog.New(nil, nil, nil))
	}, 28},
}

// retrySession makes a Session, signs it in as Lan and fetches its likes
// once.
func retrySession(session func() (*Session, chan func())) (*Session, chan func()) {
	s, q := session()
	s.KeepAccount(catalog.Account{Name: "Lan"}, catalog.Library{})
	retryLikes(s, q)
	return s, q
}

// BenchmarkRetry measures fetching and seeding the likes again.
func BenchmarkRetry(b *testing.B) {
	for _, tc := range retryCases {
		b.Run(tc.name, func(b *testing.B) {
			s, q := retrySession(tc.session)
			b.ReportAllocs()
			for b.Loop() {
				retryLikes(s, q)
			}
		})
	}
}

// nopCache keeps nothing, so that only the Session's cost is measured.
type nopCache struct{}

func (nopCache) Load() (accountcache.Saved, error) { return accountcache.Saved{}, fs.ErrNotExist }
func (nopCache) Save(*accountcache.Saved) error    { return nil }
func (nopCache) Remove() error                     { return nil }

// cachedSession is a Session signed in as Lan, using nopCache, and a cache
// of 5000 liked songs as accountcache loads them.
func cachedSession() (*Session, chan func(), accountcache.Saved) {
	s, q := lanSession()
	return s, q, lanCached()
}

// lanSession is a Session signed in as Lan, using nopCache, with nothing
// shown yet.
func lanSession() (*Session, chan func()) {
	s, q := newSession(catalog.New(nil, nil, nil))
	s.cache = nopCache{}
	s.setAccount(catalog.Account{Name: "Lan"})
	return s, q
}

// lanCached is a cache of 5000 liked songs as accountcache loads them.
func lanCached() accountcache.Saved {
	liked := catalogtest.Decoded(5000)
	catalog.ShareStrings(liked)
	ids := make([]string, len(liked))
	for i, song := range liked {
		ids[i] = song.ID
	}
	return accountcache.Saved{Account: catalog.Account{Name: "Lan"}, Liked: ids, LikedSongs: liked}
}

// lanSessions makes n lanSessions, as a start has.
func lanSessions(n int) []*Session {
	sessions := make([]*Session, n)
	for i := range sessions {
		sessions[i], _ = lanSession()
	}
	return sessions
}

// BenchmarkCacheRead is showing the cache at a start, on the UI thread,
// once a run: into an empty catalog and a Session's first likes.
func BenchmarkCacheRead(b *testing.B) {
	saved := lanCached()
	benchFresh(b, lanSessions, func(s *Session) { s.cacheRead(saved, nil) })
}

// BenchmarkSaveCache is what the UI thread does to save once fetched.
func BenchmarkSaveCache(b *testing.B) {
	s, q, saved := cachedSession()
	s.cacheRead(saved, nil)
	s.isLibraryFresh, s.isLikesFresh = true, true
	b.ReportAllocs()
	for b.Loop() {
		saveRoundTrip(s, q)
	}
}

// saveRoundTrip is a save and its end.
func saveRoundTrip(s *Session, q chan func()) {
	s.saveCache()
	(<-q)()
}
