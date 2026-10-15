package session

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/catalog/catalogtest"
	"yt-music-player/internal/fetchstate"
	"yt-music-player/internal/loader"
	"yt-music-player/internal/notice"
)

// posts stands in for the UI thread: the test runs queued results on its
// own goroutine.
type posts chan func()

// fixture is a Session over catalogtest.Accounts, which the test answers.
type fixture struct {
	src    catalogtest.Accounts
	s      *Session
	states fetchstate.Registry
	load   *loader.Loader
	cat    *catalog.Catalog
	q      posts
}

func newFixture() *fixture {
	f := &fixture{
		src: catalogtest.Accounts{Source: catalogtest.New()}, cat: catalog.New(nil, nil, nil),
		q: make(posts, 16),
	}
	post := func(fn func()) { f.q <- fn }
	f.load = loader.New(f.src, &f.states, f.cat, post)
	f.s = New(f.src, &f.states, f.load, f.cat, post)
	// Loaded so changed can see an account change forget it.
	f.states.Set(loader.HomeFeed, fetchstate.Fetch{State: fetchstate.Loaded})
	return f
}

// changed reports whether an account change forgot Home.
func (f *fixture) changed() bool { return f.states.State(loader.HomeFeed).State == fetchstate.Idle }

// held waits for one result and returns it unrun.
func (f *fixture) held(t *testing.T) func() {
	t.Helper()
	select {
	case fn := <-f.q:
		return fn
	case <-time.After(5 * time.Second):
		t.Fatal("no result came back")
		return nil
	}
}

func (f *fixture) drain() {
	for {
		select {
		case fn := <-f.q:
			fn()
		default:
			return
		}
	}
}

func (f *fixture) settle(t *testing.T) {
	t.Helper()
	for f.s.IsBusy() {
		f.held(t)()
	}
	f.drain()
}

func (f *fixture) take(t *testing.T, want ...string) map[string]catalogtest.Call {
	t.Helper()
	return f.src.Take(t, nil, want...)
}

func (f *fixture) next(t *testing.T, kind, id string) catalogtest.Call {
	t.Helper()
	return f.src.Next(t, nil, kind, id)
}

func (f *fixture) none(t *testing.T) {
	t.Helper()
	f.src.None(t, nil, 50*time.Millisecond)
}

func wantState(t *testing.T, f *fixture, res fetchstate.Resource, want fetchstate.State) {
	t.Helper()
	if got := f.states.State(res); got.State != want {
		t.Fatalf("%+v is %d (%v), want %d", res, got.State, got.Err, want)
	}
}

// wantNotices compares kind, title and topic, and detail when wanted.
func wantNotices(t *testing.T, f *fixture, want ...notice.Notice) {
	t.Helper()
	got := f.s.TakeNotices()
	if len(got) != len(want) {
		t.Fatalf("notices %+v, want %+v", got, want)
	}
	for i := range got {
		g, w := got[i], want[i]
		isSame := g.Kind == w.Kind && g.Title == w.Title && g.Topic == w.Topic
		isDetailSame := w.Detail == "" || g.Detail == w.Detail
		if !isSame || !isDetailSame {
			t.Errorf("notice %+v, want %+v", g, w)
		}
	}
}

var (
	ada      = catalog.Account{Name: "Ada Lovelace", Handle: "@ada"}
	adaMix   = catalog.Playlist{ID: "ada-mix", Name: "Engine Notes", Songs: []string{"far-1", "far-2"}}
	tracks   = []catalog.Song{{ID: "far-1", Title: "Carrier Wave"}, {ID: "far-2", Title: "Dead Air"}}
	adaLikes = catalog.Playlist{ID: catalog.LikedID, Songs: []string{"far-2", "far-1"}}
)

// adaLibrary has duplicates and is made anew each call, because keeping
// it dedupes in place.
func adaLibrary() catalog.Library {
	return catalog.Library{
		Playlists: []catalog.Playlist{adaMix, adaMix},
		Artists:   []catalog.Artist{{Name: "Nova Static"}, {Name: "Nova Static"}, {ID: "UCnova", Name: "Nova Static"}},
		Songs:     slices.Clone(tracks),
	}
}

func signedIn(t *testing.T) *fixture {
	t.Helper()
	f := newFixture()
	f.s.Start()
	calls := f.take(t, "account", "library", "playlist LM")
	calls["account"].Reply <- catalogtest.Reply{Account: ada}
	calls["library"].Reply <- catalogtest.Reply{Library: adaLibrary()}
	calls["playlist LM"].Reply <- catalogtest.Reply{Playlist: adaLikes, Songs: []catalog.Song{tracks[1], tracks[0]}}
	f.settle(t)
	return f
}

func signedOut(t *testing.T) *fixture {
	t.Helper()
	f := newFixture()
	f.s.Start()
	calls := f.take(t, "account", "library", "playlist LM")
	// Answer one first: it signs out everywhere, stopping the other
	// fetches. Answering all at once could post one after settle, which the
	// test's next held would then run instead of its own result.
	calls["account"].Reply <- catalogtest.Reply{Err: catalog.ErrSignedOut}
	f.held(t)()
	for _, call := range []string{"library", "playlist LM"} {
		calls[call].Reply <- catalogtest.Reply{Err: catalog.ErrSignedOut}
	}
	f.settle(t)
	return f
}

func TestStartFetchesOnce(t *testing.T) {
	f := signedIn(t)
	for _, res := range Resources {
		wantState(t, f, res, fetchstate.Loaded)
	}
	if !f.s.IsSignedIn() || f.s.Account() != ada || f.s.DisplayName() != "Ada Lovelace (@ada)" {
		t.Errorf("signed in as %+v, %q", f.s.Account(), f.s.DisplayName())
	}
	// Repeats dropped; artists told apart by channel, else by name.
	if lib := f.cat.LibraryPlaylists(); len(lib) != 1 || lib[0].ID != "ada-mix" || len(f.s.Library().Artists) != 2 {
		t.Errorf("library playlists %+v, artists %+v", lib, f.s.Library().Artists)
	}
	if f.s.Library().Playlists != nil || len(f.s.Library().Songs) != 2 {
		t.Errorf("library kept %+v", f.s.Library())
	}
	if !f.s.Liked("far-1") || !f.s.Liked("far-2") || f.s.LikedCount() != 2 {
		t.Error("likes not seeded from Liked music")
	}
	if got := f.s.LikedSongs(); len(got) != 2 || got[0].ID != "far-2" {
		t.Errorf("liked songs %+v, want newest first", got)
	}
	if f.changed() {
		t.Error("fetching the account changed it")
	}
	f.s.Start()
	f.none(t)
}

// seedLikes must not alias its argument.
func TestLiked(t *testing.T) {
	s := newFixture().s
	if s.Liked("a") {
		t.Fatal("a new session likes a song")
	}
	s.seedLikes([]string{"a", "b"})
	s.applyLike("c", true)
	s.applyLike("c", true)
	s.applyLike("a", false)
	if s.Liked("a") || !s.Liked("b") || !s.Liked("c") {
		t.Fatal("applyLike did not set likes")
	}
	ids := []string{"d"}
	s.seedLikes(ids)
	ids[0] = "a"
	if s.Liked("b") || s.Liked("c") || s.Liked("a") || !s.Liked("d") {
		t.Error("seedLikes did not replace the likes, or follows its argument")
	}
	s.seedLikes(nil)
	if s.Liked("d") {
		t.Error("seedLikes(nil) left a like")
	}
	s.applyLike("a", true)
	if !s.Liked("a") {
		t.Error("liking after seedLikes(nil) failed")
	}
}

func TestNoLikedMusic(t *testing.T) {
	f := newFixture()
	f.s.Start()
	calls := f.take(t, "account", "library", "playlist LM")
	calls["account"].Reply <- catalogtest.Reply{Account: ada}
	calls["library"].Reply <- catalogtest.Reply{}
	// An account that never liked a song may have no such playlist.
	calls["playlist LM"].Reply <- catalogtest.Reply{Err: catalog.ErrNotFound}
	f.settle(t)
	wantState(t, f, LikedRes, fetchstate.Loaded)
}

func TestSignedOutEverywhere(t *testing.T) {
	f := newFixture()
	f.s.Start()
	calls := f.take(t, "account", "library", "playlist LM")
	// The first ErrSignedOut signs out everywhere, and stops the rest.
	calls["account"].Reply <- catalogtest.Reply{Err: catalog.ErrSignedOut}
	f.held(t)()
	for _, res := range Resources {
		wantState(t, f, res, fetchstate.SignedOut)
	}
	if calls["library"].Ctx.Err() == nil || calls["playlist LM"].Ctx.Err() == nil || f.s.IsBusy() {
		t.Error("signing out left the account's fetches running")
	}
	for _, call := range []string{"library", "playlist LM"} {
		calls[call].Reply <- catalogtest.Reply{Err: calls[call].Ctx.Err()}
	}
	// No one was signed in, so Home stays.
	wantState(t, f, loader.HomeFeed, fetchstate.Loaded)

	// Liking asks to sign in, and asks the source nothing.
	f.s.SetLiked("far-1", true)
	if f.s.Liked("far-1") {
		t.Error("liked while signed out")
	}
	wantNotices(t, f, notice.Notice{Kind: notice.NeedsAccount, Title: "Sign in to like songs", Topic: notice.SignIn})
	f.none(t)
	f.s.Start()
	f.none(t)
}

func TestLikeSignedOutMeanwhile(t *testing.T) {
	f := signedIn(t)
	f.s.SetLiked("far-1", false)
	f.next(t, "like", "-far-1").Reply <- catalogtest.Reply{Err: catalog.ErrSignedOut}
	f.held(t)()
	if f.s.IsSignedIn() || f.s.Liked("far-2") || len(f.cat.LibraryPlaylists()) != 0 || len(f.s.Library().Songs) != 0 {
		t.Error("signed out, the account's library or likes stayed")
	}
	wantState(t, f, LibraryRes, fetchstate.SignedOut)
	// Home was Ada's, so it is refetched.
	wantState(t, f, loader.HomeFeed, fetchstate.Idle)
	wantNotices(t, f, notice.Notice{Kind: notice.NeedsAccount, Title: "Sign in to like songs", Topic: notice.SignIn})
}

func TestLibraryFailsAndRetries(t *testing.T) {
	f := newFixture()
	f.s.Start()
	calls := f.take(t, "account", "library", "playlist LM")
	calls["account"].Reply <- catalogtest.Reply{Err: errors.New("offline")}
	calls["library"].Reply <- catalogtest.Reply{Err: errors.New("network is down")}
	calls["playlist LM"].Reply <- catalogtest.Reply{Playlist: adaLikes, Songs: tracks}
	f.settle(t)
	wantState(t, f, LibraryRes, fetchstate.Failed)
	if err := f.states.State(LibraryRes).Err; err == nil || err.Error() != "network is down" {
		t.Errorf("error %v", err)
	}
	// Failures are retried only by the user.
	f.s.Start()
	f.none(t)
	// Retrying retries the failed account too.
	f.s.Retry(LibraryRes)
	wantState(t, f, LibraryRes, fetchstate.Loading)
	calls = f.take(t, "library", "account")
	calls["library"].Reply <- catalogtest.Reply{Library: adaLibrary()}
	calls["account"].Reply <- catalogtest.Reply{Account: ada}
	f.settle(t)
	wantState(t, f, LibraryRes, fetchstate.Loaded)
	wantState(t, f, AccountRes, fetchstate.Loaded)
	f.s.Retry(LikedRes)
	f.next(t, "playlist", "LM").Reply <- catalogtest.Reply{Playlist: adaLikes}
	f.settle(t)
	f.none(t)
}

func TestLikeAppliesAtOnceAndReverts(t *testing.T) {
	f := signedIn(t)
	gen := f.s.LikesGen()
	f.s.SetLiked("far-2", false)
	// Unliking takes the song off at once, before the source answers.
	if f.s.Liked("far-2") || f.s.LikedCount() != 1 || f.s.LikesGen() == gen {
		t.Fatal("unliking waited for the source")
	}
	unlike := f.next(t, "like", "-far-2")
	// Liking again what is liked asks nothing.
	f.s.SetLiked("far-1", true)
	f.none(t)
	gen = f.s.LikesGen()
	unlike.Reply <- catalogtest.Reply{Err: errors.New("quota exceeded")}
	f.held(t)()
	if !f.s.Liked("far-2") || f.s.LikesGen() == gen {
		t.Error("a failed unlike stayed")
	}
	// It kept its place.
	if got := f.s.LikedSongs(); len(got) != 2 || got[0].ID != "far-2" {
		t.Errorf("liked songs %+v", got)
	}
	wantNotices(t, f, notice.Notice{Kind: notice.Failure, Title: "Couldn’t unlike “Dead Air”", Detail: "quota exceeded"})

	// A song the catalog lacks is counted but not listed, and its
	// failure has no title.
	f.s.SetLiked("unknown", true)
	if f.s.LikedSongs()[0].ID == "unknown" || f.s.LikedCount() != 3 {
		t.Error("a song the catalog lacks is listed")
	}
	f.next(t, "like", "+unknown").Reply <- catalogtest.Reply{Err: errors.New("no such song")}
	f.held(t)()
	wantNotices(t, f, notice.Notice{Kind: notice.Failure, Title: "Couldn’t like the song"})
}

func TestLaterLikeDecides(t *testing.T) {
	f := signedIn(t)
	f.s.SetLiked("far-1", false)
	first := f.next(t, "like", "-far-1")
	f.s.SetLiked("far-1", true)
	second := f.next(t, "like", "+far-1")
	first.Reply <- catalogtest.Reply{Err: errors.New("late")}
	f.held(t)()
	if !f.s.Liked("far-1") {
		t.Error("an outdated like's failure reverted the later one")
	}
	second.Reply <- catalogtest.Reply{}
	f.settle(t)
	if !f.s.Liked("far-1") || len(f.s.TakeNotices()) != 0 {
		t.Error("far-1 unliked")
	}
}

func TestPendingLikeOutlivesLikedMusic(t *testing.T) {
	f := newFixture()
	f.s.Start()
	calls := f.take(t, "account", "library", "playlist LM")
	calls["account"].Reply <- catalogtest.Reply{Account: ada}
	f.held(t)()
	// A like sent meanwhile is newer than the list.
	f.s.SetLiked("midnight-arcade", true)
	like := f.next(t, "like", "+midnight-arcade")
	calls["playlist LM"].Reply <- catalogtest.Reply{Playlist: adaLikes, Songs: tracks}
	f.held(t)()
	if !f.s.Liked("far-1") || !f.s.Liked("far-2") || !f.s.Liked("midnight-arcade") {
		t.Fatal("likes not seeded from Liked music")
	}
	if !slices.Equal(f.s.likeOrder, []string{"midnight-arcade", "far-2", "far-1"}) {
		t.Errorf("likes %q, want newest first", f.s.likeOrder)
	}
	like.Reply <- catalogtest.Reply{}
	calls["library"].Reply <- catalogtest.Reply{}
	f.settle(t)
	if !f.s.Liked("midnight-arcade") {
		t.Error("a like that went through was undone")
	}
}

func TestSignIn(t *testing.T) {
	f := signedOut(t)
	if !f.s.CanSignIn() {
		t.Fatal("Accounts cannot sign in")
	}
	// Blank credentials send nothing.
	f.s.SignIn(" \n ")
	f.none(t)
	f.s.SignIn("Cookie: SID=bad;\n HSID=worse")
	signIn := f.next(t, "signin", "SID=bad; HSID=worse")
	if !f.s.IsSigningIn() || !f.s.IsBusy() {
		t.Error("not signing in")
	}
	signIn.Reply <- catalogtest.Reply{Err: errors.New("these cookies are signed out")}
	f.held(t)()
	wantNotices(t, f, notice.Notice{Kind: notice.SignInFailed, Title: "Couldn’t sign in", Detail: "these cookies are signed out"})
	if f.s.IsSignedIn() || f.s.IsSigningIn() || f.changed() {
		t.Error("a failed sign-in signed in")
	}

	f.s.SignIn("SID=good")
	f.next(t, "signin", "SID=good").Reply <- catalogtest.Reply{Account: ada}
	f.held(t)()
	wantNotices(t, f, notice.Notice{Kind: notice.SignedIn, Title: "Signed in as Ada Lovelace"})
	if !f.s.IsSignedIn() || f.s.Account() != ada || !f.changed() {
		t.Error("not signed in as Ada")
	}
	// Everything the account has is refetched, including Home.
	wantState(t, f, loader.HomeFeed, fetchstate.Idle)
	calls := f.take(t, "account", "library", "playlist LM")
	calls["account"].Reply <- catalogtest.Reply{Account: ada}
	calls["library"].Reply <- catalogtest.Reply{Library: adaLibrary()}
	calls["playlist LM"].Reply <- catalogtest.Reply{Playlist: adaLikes, Songs: tracks}
	f.settle(t)
	if len(f.cat.LibraryPlaylists()) != 1 || !f.s.Liked("far-1") {
		t.Error("Ada's library or likes missing")
	}
}

func TestSignInCanceled(t *testing.T) {
	f := signedOut(t)
	f.s.SignIn("SID=slow")
	signIn := f.next(t, "signin", "SID=slow")
	f.s.CancelSignIn()
	if signIn.Ctx.Err() == nil || f.s.IsSigningIn() {
		t.Fatal("canceling left it signing in")
	}
	signIn.Reply <- catalogtest.Reply{Err: signIn.Ctx.Err()}
	f.none(t)
	f.drain()
	if f.s.IsSignedIn() || len(f.s.TakeNotices()) != 0 {
		t.Error("a sign-in given up on still told")
	}

	// Credentials stored despite the cancellation still sign in.
	f.s.SignIn("SID=raced")
	signIn = f.next(t, "signin", "SID=raced")
	f.s.CancelSignIn()
	signIn.Reply <- catalogtest.Reply{Account: ada}
	f.held(t)()
	if !f.s.IsSignedIn() {
		t.Error("credentials stored did not sign in")
	}
	wantNotices(t, f, notice.Notice{Kind: notice.SignedIn, Title: "Signed in as Ada Lovelace"})
}

func TestSignInWithoutAccounts(t *testing.T) {
	src := catalogtest.New()
	var states fetchstate.Registry
	s := New(src, &states, loader.New(src, &states, nil, nil), nil, nil)
	if s.CanSignIn() {
		t.Error("a source without accounts signs in")
	}
	s.SignIn("SID=good")
	s.SignOut()
	src.None(t, nil, 50*time.Millisecond)
}

func TestSignOut(t *testing.T) {
	f := signedIn(t)
	f.s.SignOut()
	signOut := f.next(t, "signout", "")
	// Until the credentials are gone Ada stays signed in, and a second
	// SignOut asks nothing.
	f.s.SignOut()
	f.none(t)
	if !f.s.IsSignedIn() || !f.s.IsSigningOut() {
		t.Error("signed out before the source did")
	}
	signOut.Reply <- catalogtest.Reply{}
	f.held(t)()
	if f.s.IsSignedIn() || f.s.IsSigningOut() || f.s.DisplayName() != "" || f.s.Liked("far-2") ||
		len(f.cat.LibraryPlaylists()) != 0 || len(f.s.Library().Songs) != 0 || f.s.LikedCount() != 0 {
		t.Error("signing out kept the account, its likes or its library")
	}
	for _, res := range Resources {
		wantState(t, f, res, fetchstate.SignedOut)
	}
	wantState(t, f, loader.HomeFeed, fetchstate.Idle)
	wantNotices(t, f, notice.Notice{Kind: notice.Info, Title: "Signed out"})
}

func TestSignOutFails(t *testing.T) {
	f := signedIn(t)
	f.s.SignOut()
	f.next(t, "signout", "").Reply <- catalogtest.Reply{Err: errors.New("keychain locked")}
	f.held(t)()
	if !f.s.IsSignedIn() || len(f.cat.LibraryPlaylists()) != 1 || f.changed() {
		t.Error("a failed sign-out signed out")
	}
	wantNotices(t, f, notice.Notice{Kind: notice.Failure, Title: "Couldn’t sign out", Detail: "keychain locked"})
}

func TestStaleResultsRejected(t *testing.T) {
	f := signedIn(t)
	// A like in flight as Ada signs out, and the old library's late answer,
	// must not change the next account.
	f.s.SetLiked("far-1", false)
	like := f.next(t, "like", "-far-1")
	f.s.Retry(LibraryRes)
	lib := f.next(t, "library", "")
	lib.Reply <- catalogtest.Reply{Library: adaLibrary()}
	staleLib := f.held(t)
	f.s.SignOut()
	f.next(t, "signout", "").Reply <- catalogtest.Reply{}
	f.held(t)()
	if lib.Ctx.Err() == nil {
		t.Error("signing out left the library's fetch running")
	}
	staleLib()
	like.Reply <- catalogtest.Reply{Err: errors.New("signed out meanwhile")}
	f.held(t)()
	if len(f.cat.LibraryPlaylists()) != 0 || f.s.Liked("far-1") {
		t.Fatal("a stale result changed the signed-out session")
	}
	wantState(t, f, LibraryRes, fetchstate.SignedOut)

	// Signing in as someone else: the old account's fetch is dropped too.
	f.s.SignIn("SID=grace")
	grace := catalog.Account{Name: "Grace Hopper"}
	f.next(t, "signin", "SID=grace").Reply <- catalogtest.Reply{Account: grace}
	f.held(t)()
	calls := f.take(t, "account", "library", "playlist LM")
	calls["library"].Reply <- catalogtest.Reply{Library: adaLibrary()}
	stale := f.held(t)
	f.s.Retry(LibraryRes)
	if calls["library"].Ctx.Err() == nil {
		t.Error("fetching anew left the old fetch running")
	}
	retry := f.next(t, "library", "")
	stale()
	if len(f.cat.LibraryPlaylists()) != 0 {
		t.Error("an outdated library was kept")
	}
	wantState(t, f, LibraryRes, fetchstate.Loading)
	retry.Reply <- catalogtest.Reply{Library: catalog.Library{}}
	calls["account"].Reply <- catalogtest.Reply{Account: grace}
	calls["playlist LM"].Reply <- catalogtest.Reply{}
	f.settle(t)
	if f.s.Account() != grace || len(f.cat.LibraryPlaylists()) != 0 {
		t.Errorf("Grace's account wrong: %+v", f.s.Account())
	}
}

func TestStop(t *testing.T) {
	f := newFixture()
	f.s.Start()
	calls := f.take(t, "account", "library", "playlist LM")
	f.s.SignIn("SID=slow")
	signIn := f.next(t, "signin", "SID=slow")
	f.s.Stop()
	for name, call := range calls {
		if call.Ctx.Err() == nil {
			t.Errorf("%s still running", name)
		}
		call.Reply <- catalogtest.Reply{Err: call.Ctx.Err()}
	}
	if signIn.Ctx.Err() == nil || f.s.IsBusy() {
		t.Error("stopping left work running")
	}
	signIn.Reply <- catalogtest.Reply{Err: signIn.Ctx.Err()}
	f.none(t)
	f.drain()
}

func TestCleanCredentials(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"SID=a; HSID=b", "SID=a; HSID=b"},
		{"Cookie: SID=a;\n  HSID=b\n", "SID=a; HSID=b"},
		{"cookie:SID=a", "SID=a"},
		{" \n\t", ""},
		{"Cookie:", ""},
	} {
		if got := CleanCredentials(tc.in); got != tc.want {
			t.Errorf("CleanCredentials(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// FuzzCollapseSpace holds collapseSpace to what it stands for.
func FuzzCollapseSpace(f *testing.F) {
	for _, s := range []string{
		"", " ", "a", "a b", " a  b ", "a\tb", "a\nb\n", "a \u00a0b", "\u2003a\u2003", "a\xffb \xff", "a\u0085b", "Cookie: SID=a;\n  HSID=b\n",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if got, want := collapseSpace(s), strings.Join(strings.Fields(s), " "); got != want {
			t.Errorf("collapseSpace(%q) = %q, want %q", s, got, want)
		}
	})
}
