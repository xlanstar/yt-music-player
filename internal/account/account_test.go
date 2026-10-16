package account

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/catalog/catalogtest"
)

// posts stands in for the Account's thread: the test runs queued results
// on its own goroutine.
type posts chan func()

// fixture is an Account over catalogtest.Accounts, which the test answers.
// log has its changes and the done calls, in order.
type fixture struct {
	src catalogtest.Accounts
	a   *Account
	q   posts
	log []any
}

// Done calls in the log.
type (
	liked    struct{ err error }
	signedIn struct {
		account catalog.Account
		err     error
	}
	signedOut struct{ err error }
)

func newFixture() *fixture {
	f := &fixture{src: catalogtest.Accounts{Source: catalogtest.New()}, q: make(posts, 16)}
	f.a = New(f.src, func(fn func()) { f.q <- fn }, func(c Change) { f.log = append(f.log, c) })
	return f
}

func (f *fixture) like(id string, isLiked bool) {
	f.a.Like(id, isLiked, func(err error) { f.log = append(f.log, liked{err}) })
}

func (f *fixture) signIn(credentials string) {
	f.a.SignIn(credentials, func(account catalog.Account, err error) {
		f.log = append(f.log, signedIn{account, err})
	})
}

func (f *fixture) signOut() { f.a.SignOut(func(err error) { f.log = append(f.log, signedOut{err}) }) }

// run waits for one result and runs it.
func (f *fixture) run(t *testing.T) {
	t.Helper()
	select {
	case fn := <-f.q:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("no result came back")
	}
}

func (f *fixture) none(t *testing.T) {
	t.Helper()
	f.src.None(t, nil, 50*time.Millisecond)
	select {
	case <-f.q:
		t.Fatal("a result came back")
	default:
	}
}

// want checks the log since the last call.
func (f *fixture) want(t *testing.T, want ...any) {
	t.Helper()
	if !reflect.DeepEqual(f.log, want) {
		t.Errorf("log %+v, want %+v", f.log, want)
	}
	f.log = nil
}

func status(account catalog.Account, isSwitched bool) Change {
	return Change{Status: &Status{Account: account, SignedIn: true, Switched: isSwitched}}
}

var (
	noOne    = Change{Status: &Status{}}
	ada      = catalog.Account{Name: "Ada Lovelace", Handle: "@ada"}
	tracks   = []catalog.Song{{ID: "far-1", Title: "Carrier Wave"}, {ID: "far-2", Title: "Dead Air"}}
	adaLikes = catalog.Playlist{ID: catalog.LikedID, Songs: []string{"far-2", "far-1"}}
)

func TestFetch(t *testing.T) {
	f := newFixture()
	f.a.Fetch(Profile)
	f.src.Next(t, nil, "account", "").Reply <- catalogtest.Reply{Account: ada}
	f.run(t)
	f.want(t, status(ada, false))

	lib := catalog.Library{Songs: tracks}
	f.a.Fetch(Library)
	f.src.Next(t, nil, "library", "").Reply <- catalogtest.Reply{Library: lib}
	f.run(t)
	f.want(t, Change{Library: &lib})

	songs := []catalog.Song{tracks[1], tracks[0]}
	f.a.Fetch(Liked)
	f.src.Next(t, nil, "playlist", "LM").Reply <- catalogtest.Reply{Playlist: adaLikes, Songs: songs}
	f.run(t)
	f.want(t, Change{Likes: &Likes{Full: true, IDs: adaLikes.Songs, Songs: songs}})

	// Without the playlist's IDs, its songs say which are liked.
	f.a.Fetch(Liked)
	f.src.Next(t, nil, "playlist", "LM").Reply <- catalogtest.Reply{Songs: tracks}
	f.run(t)
	f.want(t, Change{Likes: &Likes{Full: true, IDs: []string{"far-1", "far-2"}, Songs: tracks}})

	// An account that never liked a song may have no such playlist.
	f.a.Fetch(Liked)
	f.src.Next(t, nil, "playlist", "LM").Reply <- catalogtest.Reply{Err: catalog.ErrNotFound}
	f.run(t)
	f.want(t, Change{Likes: &Likes{Full: true}})
	if f.a.IsBusy() {
		t.Error("busy after the fetches")
	}
}

func TestFetchFails(t *testing.T) {
	f := newFixture()
	f.a.Fetch(Library)
	first := f.src.Next(t, nil, "library", "")
	// Fetching again drops the fetch in flight.
	f.a.Fetch(Library)
	if first.Ctx.Err() == nil || !f.a.IsBusy() {
		t.Error("fetching again left the first fetch running")
	}
	first.Reply <- catalogtest.Reply{Library: catalog.Library{Songs: tracks}}
	offline := errors.New("network is down")
	f.src.Next(t, nil, "library", "").Reply <- catalogtest.Reply{Err: offline}
	f.run(t)
	f.want(t, Change{Failed: &Failure{Part: Library, Err: offline}})
	f.none(t)
}

func TestSignedOut(t *testing.T) {
	f := newFixture()
	for p := range f.a.fetches {
		f.a.Fetch(Part(p))
	}
	calls := f.src.Take(t, nil, "account", "library", "playlist LM")
	// The first ErrSignedOut signs out, and stops the rest.
	calls["account"].Reply <- catalogtest.Reply{Err: catalog.ErrSignedOut}
	f.run(t)
	f.want(t, noOne)
	if calls["library"].Ctx.Err() == nil || calls["playlist LM"].Ctx.Err() == nil || f.a.IsBusy() {
		t.Error("signing out left the account's fetches running")
	}
	for _, call := range []string{"library", "playlist LM"} {
		calls[call].Reply <- catalogtest.Reply{Err: catalog.ErrSignedOut}
	}
	f.none(t)
	f.want(t)
}

func TestLike(t *testing.T) {
	f := newFixture()
	f.like("far-1", true)
	if !f.a.IsBusy() {
		t.Error("not busy liking")
	}
	f.src.Next(t, nil, "like", "+far-1").Reply <- catalogtest.Reply{}
	f.run(t)
	// The answer comes before the change it causes.
	f.want(t, liked{}, Change{Likes: &Likes{Added: []string{"far-1"}}})
	f.like("far-2", false)
	f.src.Next(t, nil, "like", "-far-2").Reply <- catalogtest.Reply{}
	f.run(t)
	f.want(t, liked{}, Change{Likes: &Likes{Removed: []string{"far-2"}}})

	// Only a like that went through changes the likes.
	quota := errors.New("quota exceeded")
	f.like("far-1", false)
	f.src.Next(t, nil, "like", "-far-1").Reply <- catalogtest.Reply{Err: quota}
	f.run(t)
	f.want(t, liked{quota})
	if f.a.IsBusy() {
		t.Error("busy after the likes")
	}
}

func TestLaterLikeDecides(t *testing.T) {
	f := newFixture()
	f.like("far-1", false)
	first := f.src.Next(t, nil, "like", "-far-1")
	f.like("far-1", true)
	second := f.src.Next(t, nil, "like", "+far-1")
	second.Reply <- catalogtest.Reply{}
	f.run(t)
	f.want(t, liked{}, Change{Likes: &Likes{Added: []string{"far-1"}}})
	// The earlier unlike, answered last, is still answered, but changes
	// nothing, nor does its ErrSignedOut sign out.
	first.Reply <- catalogtest.Reply{Err: catalog.ErrSignedOut}
	f.run(t)
	f.want(t, liked{catalog.ErrSignedOut})
}

func TestLikeSignedOut(t *testing.T) {
	f := newFixture()
	f.a.Fetch(Library)
	lib := f.src.Next(t, nil, "library", "")
	f.like("far-1", true)
	f.src.Next(t, nil, "like", "+far-1").Reply <- catalogtest.Reply{Err: catalog.ErrSignedOut}
	f.run(t)
	f.want(t, liked{catalog.ErrSignedOut}, noOne)
	if lib.Ctx.Err() == nil {
		t.Error("signing out left the library's fetch running")
	}
}

func TestStaleLikes(t *testing.T) {
	f := newFixture()
	f.like("far-1", true)
	like := f.src.Next(t, nil, "like", "+far-1")
	f.signOut()
	f.src.Next(t, nil, "signout", "").Reply <- catalogtest.Reply{}
	f.run(t)
	f.want(t, noOne, signedOut{})
	if f.a.IsBusy() {
		t.Error("the previous credentials' like is in flight")
	}
	// The previous credentials' like changes nothing.
	like.Reply <- catalogtest.Reply{}
	f.run(t)
	f.want(t, liked{})

	f.like("far-2", true)
	like = f.src.Next(t, nil, "like", "+far-2")
	f.signIn("SID=grace")
	grace := catalog.Account{Name: "Grace Hopper"}
	f.src.Next(t, nil, "signin", "SID=grace").Reply <- catalogtest.Reply{Account: grace}
	f.run(t)
	f.want(t, status(grace, true), signedIn{grace, nil})
	f.src.Take(t, nil, "account", "library", "playlist LM")
	like.Reply <- catalogtest.Reply{Err: catalog.ErrSignedOut}
	f.run(t)
	f.want(t, liked{catalog.ErrSignedOut})
}

func TestSignIn(t *testing.T) {
	f := newFixture()
	if !f.a.CanSignIn() {
		t.Fatal("Accounts cannot sign in")
	}
	// Blank credentials send nothing.
	f.signIn(" \n ")
	f.none(t)
	f.signIn("Cookie: SID=bad;\n HSID=worse")
	signIn := f.src.Next(t, nil, "signin", "SID=bad; HSID=worse")
	if !f.a.IsSigningIn() || !f.a.IsBusy() {
		t.Error("not signing in")
	}
	bad := errors.New("these cookies are signed out")
	signIn.Reply <- catalogtest.Reply{Err: bad}
	f.run(t)
	f.want(t, signedIn{err: bad})
	if f.a.IsSigningIn() {
		t.Error("still signing in")
	}

	f.signIn("SID=good")
	f.src.Next(t, nil, "signin", "SID=good").Reply <- catalogtest.Reply{Account: ada}
	f.run(t)
	f.want(t, status(ada, true), signedIn{ada, nil})
	// Everything the account has is fetched anew.
	calls := f.src.Take(t, nil, "account", "library", "playlist LM")
	calls["account"].Reply <- catalogtest.Reply{Account: ada}
	f.run(t)
	f.want(t, status(ada, false))
}

func TestSignInCanceled(t *testing.T) {
	f := newFixture()
	f.signIn("SID=slow")
	signIn := f.src.Next(t, nil, "signin", "SID=slow")
	f.a.CancelSignIn()
	if signIn.Ctx.Err() == nil || f.a.IsSigningIn() {
		t.Fatal("canceling left it signing in")
	}
	signIn.Reply <- catalogtest.Reply{Err: signIn.Ctx.Err()}
	f.none(t)
	f.want(t)

	// A sign-in cancels the one in flight.
	f.signIn("SID=raced")
	raced := f.src.Next(t, nil, "signin", "SID=raced")
	f.signIn("SID=later")
	later := f.src.Next(t, nil, "signin", "SID=later")
	if raced.Ctx.Err() == nil {
		t.Fatal("signing in again left the first sign-in running")
	}
	// Credentials stored despite the cancellation still sign in, and the
	// later sign-in stays in flight.
	raced.Reply <- catalogtest.Reply{Account: ada}
	f.run(t)
	f.want(t, status(ada, true), signedIn{ada, nil})
	if !f.a.IsSigningIn() {
		t.Error("the later sign-in was given up")
	}
	f.src.Take(t, nil, "account", "library", "playlist LM")
	bad := errors.New("these cookies are signed out")
	later.Reply <- catalogtest.Reply{Err: bad}
	f.run(t)
	f.want(t, signedIn{err: bad})
	if f.a.IsSigningIn() {
		t.Error("still signing in")
	}
}

func TestSignInWithoutAccounts(t *testing.T) {
	src := catalogtest.New()
	a := New(src, nil, nil)
	if a.CanSignIn() {
		t.Error("a source without accounts signs in")
	}
	a.SignIn("SID=good", nil)
	a.SignOut(nil)
	src.None(t, nil, 50*time.Millisecond)
}

func TestSignOut(t *testing.T) {
	f := newFixture()
	f.signIn("SID=slow")
	signIn := f.src.Next(t, nil, "signin", "SID=slow")
	f.signOut()
	signOut := f.src.Next(t, nil, "signout", "")
	// A second SignOut while one is in flight asks nothing.
	f.signOut()
	f.none(t)
	if !f.a.IsSigningOut() {
		t.Error("not signing out")
	}
	keychain := errors.New("keychain locked")
	signOut.Reply <- catalogtest.Reply{Err: keychain}
	f.run(t)
	f.want(t, signedOut{keychain})
	if !f.a.IsSigningIn() {
		t.Error("a failed sign-out gave up the sign-in")
	}

	f.signOut()
	f.src.Next(t, nil, "signout", "").Reply <- catalogtest.Reply{}
	f.run(t)
	f.want(t, noOne, signedOut{})
	if f.a.IsSigningOut() || f.a.IsSigningIn() || signIn.Ctx.Err() == nil {
		t.Error("signing out left a sign-in or sign-out in flight")
	}
	signIn.Reply <- catalogtest.Reply{Err: signIn.Ctx.Err()}
	f.none(t)
}

func TestStop(t *testing.T) {
	f := newFixture()
	for p := range f.a.fetches {
		f.a.Fetch(Part(p))
	}
	calls := f.src.Take(t, nil, "account", "library", "playlist LM")
	f.signIn("SID=slow")
	signIn := f.src.Next(t, nil, "signin", "SID=slow")
	f.like("far-1", true)
	like := f.src.Next(t, nil, "like", "+far-1")
	f.a.Stop()
	calls["signin"], calls["like"] = signIn, like
	for name, call := range calls {
		if call.Ctx.Err() == nil {
			t.Errorf("%s still running", name)
		}
		call.Reply <- catalogtest.Reply{Err: call.Ctx.Err()}
	}
	if f.a.IsBusy() {
		t.Error("stopping left work running")
	}
	f.none(t)
	f.want(t)
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
