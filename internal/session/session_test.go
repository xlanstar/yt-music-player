package session

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unsafe"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/catalog/catalogtest"
	"yt-music-player/internal/fetchstate"
	"yt-music-player/internal/loader"
	"yt-music-player/internal/notice"
)

// request is one the Session made of its Daemon, which the test answers
// by calling done or signedIn, on its own goroutine as the UI thread.
type request struct {
	// what is "refresh", "like +id" or "like -id", "signin <credentials>"
	// or "signout".
	what     string
	done     func(error)
	signedIn func(catalog.Account, error)
	// song is what a like described.
	song *catalog.Song
}

// fakeDaemon records the requests a Session makes.
type fakeDaemon struct {
	requests []request
	canceled int
}

func (d *fakeDaemon) Refresh(done func(error)) {
	d.requests = append(d.requests, request{what: "refresh", done: done})
}

func (d *fakeDaemon) Like(id string, liked bool, song *catalog.Song, done func(error)) {
	sign := "-"
	if liked {
		sign = "+"
	}
	d.requests = append(d.requests, request{what: "like " + sign + id, done: done, song: song})
}

func (d *fakeDaemon) SignIn(credentials string, done func(catalog.Account, error)) {
	d.requests = append(d.requests, request{what: "signin " + credentials, signedIn: done})
}

func (d *fakeDaemon) CancelSignIn() { d.canceled++ }

func (d *fakeDaemon) SignOut(done func(error)) {
	d.requests = append(d.requests, request{what: "signout", done: done})
}

// take returns the requests made since the last take, which must be want.
func (d *fakeDaemon) take(t *testing.T, want ...string) []request {
	t.Helper()
	got := make([]string, len(d.requests))
	for i, r := range d.requests {
		got[i] = r.what
	}
	if !slices.Equal(got, want) {
		t.Fatalf("requests %q, want %q", got, want)
	}
	taken := d.requests
	d.requests = nil
	return taken
}

// next returns the one request made since the last take, which must be
// want.
func (d *fakeDaemon) next(t *testing.T, want string) request {
	t.Helper()
	return d.take(t, want)[0]
}

// fixture is a Session over a fakeDaemon and a catalogtest.Source, which
// the test answers, as the app starts: ytmd fetches who is signed in and
// tells it, and the Session fetches the rest. The test's goroutine is the
// UI thread, which runs the fetches' results (run).
type fixture struct {
	d       *fakeDaemon
	src     catalogtest.Source
	q       chan func()
	s       *Session
	fetches fetchstate.Registry
	cat     *catalog.Catalog
}

func newFixture(tb testing.TB) *fixture {
	f := &fixture{d: &fakeDaemon{}, src: catalogtest.New(tb), q: make(chan func(), 16), cat: catalog.New(nil, nil, nil)}
	// The loader only forgets Home here: it never runs its post.
	load := loader.New(catalogtest.New(tb), &f.fetches, f.cat, func(func()) { panic("the loader fetched") })
	f.s = New(Options{
		Daemon: f.d, Source: f.src, Post: func(fn func()) { f.q <- fn },
		Fetches: &f.fetches, Loader: load, Catalog: f.cat,
	})
	// Loaded so changed can see an account change forget it.
	f.fetches.Set(loader.HomeRes, fetchstate.Fetch{State: fetchstate.Loaded})
	return f
}

// run waits for one of the Session's results and runs it.
func (f *fixture) run(t *testing.T) {
	t.Helper()
	select {
	case fn := <-f.q:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("no result came back")
	}
}

// answer answers the Session's fetch of kind and id with r, and runs its
// result.
func (f *fixture) answer(t *testing.T, kind, id string, r catalogtest.Reply) {
	t.Helper()
	f.src.Next(t, nil, kind, id).Reply <- r
	f.run(t)
}

// fetches returns the Session's fetches of the library and Liked music.
func (f *fixture) takeFetches(t *testing.T) (library, liked catalogtest.Call) {
	t.Helper()
	calls := f.src.Take(t, nil, "library", "playlist LM")
	return calls["library"], calls["playlist LM"]
}

// fetched answers them as Ada's, and runs their results.
func (f *fixture) fetched(t *testing.T) {
	t.Helper()
	library, liked := f.takeFetches(t)
	library.Reply <- catalogtest.Reply{Library: adaLibrary()}
	liked.Reply <- adaLiked()
	f.run(t)
	f.run(t)
}

// none fails t if the Session fetches anything.
func (f *fixture) none(t *testing.T) {
	t.Helper()
	f.src.None(t, nil, 30*time.Millisecond)
}

// changed reports whether an account change forgot Home.
func (f *fixture) changed() bool { return f.fetches.Get(loader.HomeRes).State == fetchstate.Idle }

func wantState(t *testing.T, f *fixture, res fetchstate.Resource, want fetchstate.State) {
	t.Helper()
	if got := f.fetches.Get(res); got.State != want {
		t.Fatalf("%+v is %d (%v), want %d", res, got.State, got.Err, want)
	}
}

func wantNotices(t *testing.T, f *fixture, want ...notice.Notice) {
	t.Helper()
	if got := f.s.TakeNotices(); !reflect.DeepEqual(got, want) {
		t.Errorf("notices %+v, want %+v", got, want)
	}
}

var (
	ada    = catalog.Account{Name: "Ada Lovelace", Handle: "@ada", ID: "ada-sign-in"}
	grace  = catalog.Account{Name: "Grace Hopper", ID: "grace-sign-in"}
	adaMix = catalog.Playlist{ID: "ada-mix", Name: "Engine Notes", Songs: []string{"far-1", "far-2"}}
	tracks = []catalog.Song{{ID: "far-1", Title: "Carrier Wave"}, {ID: "far-2", Title: "Dead Air"}}

	signInToLikeNotice = notice.Notice{Kind: notice.KindNeedsAccount, Title: notice.SignInToLike, Topic: notice.TopicSignIn}
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

// adaLiked is Ada's Liked music, newest first, as the source answers it.
func adaLiked() catalogtest.Reply {
	return catalogtest.Reply{
		Playlist: catalog.Playlist{ID: catalog.LikedID, Songs: []string{"far-2", "far-1"}},
		Songs:    []catalog.Song{tracks[1], tracks[0]},
	}
}

func signedIn(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true})
	f.fetched(t)
	if f.s.IsBusy() {
		t.Fatal("busy once fetched")
	}
	return f
}

func signedOut(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.s.ApplyAccount(api.AccountEvent{})
	return f
}

func TestFetched(t *testing.T) {
	f := newFixture(t)
	// Until ytmd tells who is signed in, as the front-ends start.
	for _, res := range Resources {
		wantState(t, f, res, fetchstate.Loading)
	}
	if !f.s.IsBusy() {
		t.Error("not busy while ytmd fetches who is signed in")
	}
	f.none(t)
	f.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true})
	wantState(t, f, AccountRes, fetchstate.Loaded)
	f.fetched(t)
	for _, res := range Resources {
		wantState(t, f, res, fetchstate.Loaded)
	}
	if !f.s.IsSignedIn() || f.s.Account() != ada || f.s.DisplayName() != "Ada Lovelace (@ada)" || f.s.IsBusy() {
		t.Errorf("signed in as %+v, %q", f.s.Account(), f.s.DisplayName())
	}
	// Repeats dropped; artists told apart by channel, else by name.
	if lib := f.cat.LibraryPlaylists(); len(lib) != 1 || lib[0].ID != "ada-mix" || len(f.s.Library().Artists) != 2 {
		t.Errorf("library playlists %+v, artists %+v", lib, f.s.Library().Artists)
	}
	if f.s.Library().Playlists != nil || len(f.s.Library().Songs) != 2 {
		t.Errorf("library kept %+v", f.s.Library())
	}
	if !f.s.IsLiked("far-1") || !f.s.IsLiked("far-2") || f.s.LikedCount() != 2 {
		t.Error("likes not seeded from Liked music")
	}
	if got := f.s.LikedSongs(); len(got) != 2 || got[0].ID != "far-2" {
		t.Errorf("liked songs %+v, want newest first", got)
	}
	if f.changed() {
		t.Error("fetching the account changed it")
	}
	f.d.take(t)
}

// Liked music answered without its IDs, or not found as for an account
// that never liked a song, still loads; its songs share their strings,
// and its IDs their songs'.
func TestFetchedLiked(t *testing.T) {
	f := newFixture(t)
	f.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true})
	library, liked := f.takeFetches(t)
	library.Reply <- catalogtest.Reply{}
	described := func() []catalog.Song {
		var songs []catalog.Song
		for _, id := range []string{"far-1", "far-2"} {
			songs = append(songs, catalog.Song{ID: id, Title: id, Artist: strings.Clone("Ada"), Album: strings.Clone("Engine")})
		}
		return songs
	}
	liked.Reply <- catalogtest.Reply{Songs: described()}
	f.run(t)
	f.run(t)
	if got := f.s.LikedSongs(); len(got) != 2 || got[0].ID != "far-1" {
		t.Fatalf("liked songs %+v", got)
	}
	a, b := f.s.LikedSongs()[0], f.s.LikedSongs()[1]
	if unsafe.StringData(a.Artist) != unsafe.StringData(b.Artist) || unsafe.StringData(a.Album) != unsafe.StringData(b.Album) {
		t.Error("liked songs keep a copy each of their artist and album")
	}

	// With its IDs, each a copy of its own, they share their songs'.
	f.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true})
	library, liked = f.takeFetches(t)
	library.Reply <- catalogtest.Reply{}
	ids := []string{strings.Clone("far-1"), strings.Clone("far-2")}
	liked.Reply <- catalogtest.Reply{Playlist: catalog.Playlist{ID: catalog.LikedID, Songs: ids}, Songs: described()}
	f.run(t)
	f.run(t)
	for _, song := range f.s.LikedSongs() {
		if i := slices.Index(f.s.likeOrder, song.ID); unsafe.StringData(f.s.likeOrder[i]) != unsafe.StringData(song.ID) {
			t.Errorf("liked %q held apart from its song's ID", song.ID)
		}
	}

	f.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true})
	library, liked = f.takeFetches(t)
	library.Reply <- catalogtest.Reply{}
	liked.Reply <- catalogtest.Reply{Err: catalog.ErrNotFound}
	f.run(t)
	f.run(t)
	wantState(t, f, LikedRes, fetchstate.Loaded)
	if f.s.LikedCount() != 0 {
		t.Errorf("%d liked, want none", f.s.LikedCount())
	}
}

// Told again who is signed in, as a reconnection's snapshot does, the
// Session fetches anew, showing what it has meanwhile and if it fails.
func TestFetchedAgain(t *testing.T) {
	f := signedIn(t)
	f.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true})
	library, liked := f.takeFetches(t)
	for _, res := range Resources {
		wantState(t, f, res, fetchstate.Loaded)
	}
	if !f.s.IsBusy() {
		t.Error("not busy fetching again")
	}
	library.Reply <- catalogtest.Reply{Err: errors.New("offline")}
	liked.Reply <- catalogtest.Reply{Songs: tracks[:1]}
	f.run(t)
	f.run(t)
	wantState(t, f, LibraryRes, fetchstate.Loaded)
	if len(f.cat.LibraryPlaylists()) != 1 || f.s.IsLiked("far-2") || !f.s.IsLiked("far-1") {
		t.Error("the library left, or the likes stayed")
	}
	// What failed meanwhile is fetched again with the rest that failed.
	f.s.RetryFailed()
	f.answer(t, "library", "", catalogtest.Reply{Library: catalog.Library{}})
	if len(f.cat.LibraryPlaylists()) != 0 || f.s.IsBusy() {
		t.Error("the library not fetched again")
	}
	f.s.RetryFailed()
	f.none(t)

	// Another account than the one shown: what it had goes at once.
	f.s.ApplyAccount(api.AccountEvent{Account: grace, SignedIn: true})
	if f.s.IsLiked("far-1") || !f.changed() {
		t.Error("Ada's likes shown as Grace's, or Home kept")
	}
	wantState(t, f, LibraryRes, fetchstate.Loading)
	f.fetched(t)
}

// Told who is signed in while fetching, as a new link's snapshot is while
// the old link's fetch fails, the Session fetches anew: the old fetch's
// failure, however late, changes nothing.
func TestFetchedAnewWhileFetching(t *testing.T) {
	f := newFixture(t)
	f.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true})
	library, liked := f.takeFetches(t)
	f.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true})
	if library.Ctx.Err() == nil || liked.Ctx.Err() == nil {
		t.Error("the old link's fetches go on")
	}
	newLibrary, newLiked := f.takeFetches(t)
	newLibrary.Reply <- catalogtest.Reply{Library: adaLibrary()}
	newLiked.Reply <- adaLiked()
	f.run(t)
	f.run(t)
	library.Reply <- catalogtest.Reply{Err: api.ErrDisconnected}
	liked.Reply <- catalogtest.Reply{Err: api.ErrDisconnected}
	select {
	case <-f.q:
		t.Error("the old link's failure came back")
	case <-time.After(30 * time.Millisecond):
	}
	for _, res := range Resources {
		wantState(t, f, res, fetchstate.Loaded)
	}
	if len(f.cat.LibraryPlaylists()) != 1 || !f.s.IsLiked("far-2") || f.s.IsBusy() {
		t.Error("the new fetches' results not shown")
	}
}

// Liked music failing to refetch shows the likes it had, and stays to fetch
// again with what failed, through its next fetch, until one goes through
// or the account changes.
func TestLikesFailedMeanwhile(t *testing.T) {
	f := signedIn(t)
	f.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true})
	library, liked := f.takeFetches(t)
	library.Reply <- catalogtest.Reply{Library: adaLibrary()}
	liked.Reply <- catalogtest.Reply{Err: errors.New("offline")}
	f.run(t)
	f.run(t)
	wantState(t, f, LikedRes, fetchstate.Loaded)
	if !f.s.IsLiked("far-2") || f.s.IsBusy() {
		t.Error("the likes left, or the session stays busy")
	}
	f.s.RetryFailed()
	liked = f.src.Next(t, nil, "playlist", catalog.LikedID)
	wantState(t, f, LikedRes, fetchstate.Loaded)
	f.s.RetryFailed()
	f.none(t)
	liked.Reply <- catalogtest.Reply{Err: errors.New("offline")}
	f.run(t)
	f.s.RetryFailed()
	f.answer(t, "playlist", catalog.LikedID, adaLiked())
	f.s.RetryFailed()
	f.none(t)

	// Failed again, then another account: nothing of Ada's is retried.
	f.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true})
	library, liked = f.takeFetches(t)
	library.Reply <- catalogtest.Reply{Library: adaLibrary()}
	liked.Reply <- catalogtest.Reply{Err: errors.New("offline")}
	f.run(t)
	f.run(t)
	f.s.ApplyAccount(api.AccountEvent{Account: grace, SignedIn: true, Switched: true})
	f.fetched(t)
	f.s.RetryFailed()
	f.none(t)
}

// The same account under another photo URL, as YouTube serves them after
// a ytmd restart, is no switch: only the photo changes.
func TestPhotoChanged(t *testing.T) {
	f := signedIn(t)
	f.s.SetLiked("far-1", false)
	like := f.d.next(t, "like -far-1")
	photo := ada
	photo.Photo = "https://yt3.example/ada-2"
	f.s.ApplyAccount(api.AccountEvent{Account: photo, SignedIn: true})
	if f.changed() || len(f.cat.LibraryPlaylists()) != 1 || !f.s.IsLiked("far-2") || f.s.IsLiked("far-1") {
		t.Error("a new photo cleared the library, the likes or Home")
	}
	if f.s.Account() != photo {
		t.Errorf("account %+v, want the new photo", f.s.Account())
	}
	wantState(t, f, LibraryRes, fetchstate.Loaded)
	f.fetched(t)
	// The like stayed pending, so the fetch's list did not undo it.
	if f.s.IsLiked("far-1") {
		t.Error("the pending unlike was dropped")
	}
	like.done(nil)
}

// A renamed account keeps its sign-in's ID: no switch, the library and
// likes stay. Another sign-in of the same name and no handle is one.
func TestRenamed(t *testing.T) {
	f := signedIn(t)
	renamed := ada
	renamed.Name, renamed.Handle = "Ada King", ""
	f.s.ApplyAccount(api.AccountEvent{Account: renamed, SignedIn: true})
	if f.changed() || len(f.cat.LibraryPlaylists()) != 1 || !f.s.IsLiked("far-2") {
		t.Error("a rename cleared the library, the likes or Home")
	}
	if f.s.DisplayName() != "Ada King" {
		t.Errorf("display name %q", f.s.DisplayName())
	}
	f.fetched(t)

	another := renamed
	another.ID = "another-sign-in"
	f.s.ApplyAccount(api.AccountEvent{Account: another, SignedIn: true})
	if !f.changed() || f.s.IsLiked("far-2") {
		t.Error("another sign-in of the same name kept the likes or Home")
	}
	f.fetched(t)
}

// Signing in after a known sign-out, as a reconnection's snapshot tells
// without Switched, refetches Home, a guest's.
func TestSignedInAfterSignedOut(t *testing.T) {
	f := signedOut(t)
	f.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true})
	if !f.changed() {
		t.Error("Home kept the guest's")
	}
	f.fetched(t)
}

// seedLikes must not alias its argument.
func TestLiked(t *testing.T) {
	s := newFixture(t).s
	if s.IsLiked("a") {
		t.Fatal("a new session likes a song")
	}
	s.seedLikes([]string{"a", "b"})
	s.applyLike("c", true)
	s.applyLike("c", true)
	s.applyLike("a", false)
	if s.IsLiked("a") || !s.IsLiked("b") || !s.IsLiked("c") {
		t.Fatal("applyLike did not set likes")
	}
	ids := []string{"d"}
	s.seedLikes(ids)
	ids[0] = "a"
	if s.IsLiked("b") || s.IsLiked("c") || s.IsLiked("a") || !s.IsLiked("d") {
		t.Error("seedLikes did not replace the likes, or follows its argument")
	}
	// A repeat keeps its newest place.
	s.seedLikes([]string{"e", "f", "e"})
	if got := s.likeOrder; !slices.Equal(got, []string{"e", "f"}) || s.LikedCount() != 2 {
		t.Errorf("seedLikes kept %q, %d liked; want [e f], 2", got, s.LikedCount())
	}
	s.seedLikes(nil)
	if s.IsLiked("d") {
		t.Error("seedLikes(nil) left a like")
	}
	s.applyLike("a", true)
	if !s.IsLiked("a") {
		t.Error("liking after seedLikes(nil) failed")
	}
}

func TestSignedOutEverywhere(t *testing.T) {
	f := signedOut(t)
	for _, res := range Resources {
		wantState(t, f, res, fetchstate.SignedOut)
	}
	if f.s.IsBusy() {
		t.Error("busy once signed out")
	}
	// No one was signed in, so Home stays.
	wantState(t, f, loader.HomeRes, fetchstate.Loaded)

	// Liking asks to sign in, and asks the daemon nothing.
	f.s.SetLiked("far-1", true)
	if f.s.IsLiked("far-1") {
		t.Error("liked while signed out")
	}
	wantNotices(t, f, signInToLikeNotice)
	f.d.take(t)
}

// A Like's ErrSignedOut comes before the Account event of the sign-out it
// caused: the first asks to sign in, the second forgets the account.
func TestLikeSignedOutMeanwhile(t *testing.T) {
	f := signedIn(t)
	f.s.SetLiked("far-1", false)
	f.d.next(t, "like -far-1").done(catalog.ErrSignedOut)
	wantNotices(t, f, signInToLikeNotice)
	if f.s.IsBusy() || !f.s.IsSignedIn() {
		t.Error("the like's answer did more than tell")
	}
	f.s.ApplyAccount(api.AccountEvent{})
	if f.s.IsSignedIn() || f.s.IsLiked("far-2") || len(f.cat.LibraryPlaylists()) != 0 || len(f.s.Library().Songs) != 0 {
		t.Error("signed out, the account's library or likes stayed")
	}
	wantState(t, f, LibraryRes, fetchstate.SignedOut)
	// Home was Ada's, so it is refetched.
	wantState(t, f, loader.HomeRes, fetchstate.Idle)
	wantNotices(t, f)
}

func TestLibraryFailsAndRetries(t *testing.T) {
	f := newFixture(t)
	f.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true})
	library, liked := f.takeFetches(t)
	library.Reply <- catalogtest.Reply{Err: &api.Error{Code: api.CodeInternal, Detail: "network is down"}}
	liked.Reply <- adaLiked()
	f.run(t)
	f.run(t)
	wantState(t, f, LibraryRes, fetchstate.Failed)
	wantState(t, f, LikedRes, fetchstate.Loaded)
	err := f.fetches.Get(LibraryRes).Err
	if !errors.Is(err, api.ErrInternal) || err.Error() != "api: ytmd failed: network is down" {
		t.Errorf("error %v", err)
	}
	if f.s.IsBusy() {
		t.Error("busy once every fetch ended")
	}
	f.s.Retry(LibraryRes)
	wantState(t, f, LibraryRes, fetchstate.Loading)
	f.answer(t, "library", "", catalogtest.Reply{Library: adaLibrary()})
	wantState(t, f, LibraryRes, fetchstate.Loaded)
	f.s.Retry(LikedRes)
	f.answer(t, "playlist", catalog.LikedID, adaLiked())
	f.d.take(t)
}

// Who is signed in failing to load, so do the library and likes, whose
// Retry asks ytmd again: its answer fetches them.
func TestAccountFailsAndRetries(t *testing.T) {
	f := newFixture(t)
	f.s.ApplyFailed(api.FailedEvent{Err: &api.Error{Code: api.CodeInternal, Detail: "offline"}})
	for _, res := range Resources {
		wantState(t, f, res, fetchstate.Failed)
	}
	if f.s.IsBusy() {
		t.Error("busy once ytmd's fetch failed")
	}
	f.none(t)
	f.s.Retry(LibraryRes)
	wantState(t, f, AccountRes, fetchstate.Loading)
	wantState(t, f, LibraryRes, fetchstate.Loading)
	f.d.next(t, "refresh").done(nil)
	f.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true})
	f.fetched(t)
	for _, res := range Resources {
		wantState(t, f, res, fetchstate.Loaded)
	}
}

func TestRefreshFails(t *testing.T) {
	f := newFixture(t)
	f.s.ApplyFailed(api.FailedEvent{Err: &api.Error{Code: api.CodeInternal}})
	f.s.Retry(LikedRes)
	f.d.next(t, "refresh").done(api.ErrDisconnected)
	for _, res := range []fetchstate.Resource{AccountRes, LikedRes} {
		if err := f.fetches.Get(res).Err; !errors.Is(err, api.ErrDisconnected) {
			t.Errorf("%+v failed with %v, want the request's failure", res, err)
		}
	}
	// An answer after the event changes nothing.
	f.s.RetryFailed()
	refresh := f.d.next(t, "refresh")
	f.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true})
	refresh.done(api.ErrDisconnected)
	wantState(t, f, AccountRes, fetchstate.Loaded)
	wantState(t, f, LikedRes, fetchstate.Loading)
	f.fetched(t)
}

func TestFailedEventChecked(t *testing.T) {
	f := signedIn(t)
	f.s.ApplyFailed(api.FailedEvent{})
	if err := f.fetches.Get(AccountRes).Err; !errors.Is(err, api.ErrInternal) {
		t.Errorf("a failure without its error is %v", err)
	}
	// Signed in, the library and likes stay.
	wantState(t, f, LibraryRes, fetchstate.Loaded)
	wantState(t, f, LikedRes, fetchstate.Loaded)
}

// A fetch answered ErrSignedOut shows signed out; ytmd tells the rest.
func TestFetchSignedOut(t *testing.T) {
	f := newFixture(t)
	f.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true})
	library, liked := f.takeFetches(t)
	library.Reply <- catalogtest.Reply{Err: catalog.ErrSignedOut}
	f.run(t)
	wantState(t, f, LibraryRes, fetchstate.SignedOut)
	f.s.ApplyAccount(api.AccountEvent{})
	if liked.Ctx.Err() == nil || f.s.IsBusy() {
		t.Error("signing out left Liked music's fetch running")
	}
	for _, res := range Resources {
		wantState(t, f, res, fetchstate.SignedOut)
	}
}

func TestLikeAppliesAtOnceAndReverts(t *testing.T) {
	f := signedIn(t)
	gen := f.s.LikesGen()
	f.s.SetLiked("far-2", false)
	// Unliking takes the song off at once, before the daemon answers.
	if f.s.IsLiked("far-2") || f.s.LikedCount() != 1 || f.s.LikesGen() == gen || !f.s.IsBusy() {
		t.Fatal("unliking waited for the daemon")
	}
	unlike := f.d.next(t, "like -far-2")
	if unlike.song != nil {
		t.Errorf("an unlike described %+v", unlike.song)
	}
	// Liking again what is liked asks nothing.
	f.s.SetLiked("far-1", true)
	f.d.take(t)
	gen = f.s.LikesGen()
	unlike.done(errors.New("quota exceeded"))
	if !f.s.IsLiked("far-2") || f.s.LikesGen() == gen || f.s.IsBusy() {
		t.Error("a failed unlike stayed")
	}
	// It kept its place.
	if got := f.s.LikedSongs(); len(got) != 2 || got[0].ID != "far-2" {
		t.Errorf("liked songs %+v", got)
	}
	wantNotices(t, f, notice.Notice{
		Kind: notice.KindFailure, Title: notice.UnlikeFailed, Detail: notice.Reason,
		Args: []string{"Dead Air", "quota exceeded"},
	})

	// A song the catalog lacks is counted but not listed, and its
	// failure has no title.
	f.s.SetLiked("unknown", true)
	if f.s.LikedSongs()[0].ID == "unknown" || f.s.LikedCount() != 3 {
		t.Error("a song the catalog lacks is listed")
	}
	like := f.d.next(t, "like +unknown")
	if like.song != nil {
		t.Errorf("a song the catalog lacks described as %+v", like.song)
	}
	like.done(errors.New("no such song"))
	wantNotices(t, f, notice.Notice{
		Kind: notice.KindFailure, Title: notice.LikeFailedSong, Detail: notice.Reason,
		Args: []string{"no such song"},
	})
}

func TestLaterLikeDecides(t *testing.T) {
	f := signedIn(t)
	f.s.SetLiked("far-1", false)
	first := f.d.next(t, "like -far-1")
	f.s.SetLiked("far-1", true)
	second := f.d.next(t, "like +far-1")
	// A like describes its song, for the other front-ends.
	if second.song == nil || *second.song != tracks[0] {
		t.Errorf("like described %+v, want %+v", second.song, tracks[0])
	}
	first.done(errors.New("late"))
	if !f.s.IsLiked("far-1") {
		t.Error("an outdated like's failure reverted the later one")
	}
	second.done(nil)
	if !f.s.IsLiked("far-1") || len(f.s.TakeNotices()) != 0 || f.s.IsBusy() {
		t.Error("far-1 unliked")
	}
}

// Likes the daemon reports apply, unless the user's own like of the song
// is pending: it is newer.
func TestLikesReported(t *testing.T) {
	f := signedIn(t)
	gen := f.s.LikesGen()
	f.s.ApplyLikes(api.Likes{Added: []string{"far-1"}})
	if f.s.LikesGen() != gen {
		t.Error("a like the session had changed the likes")
	}
	f.s.SetLiked("far-2", false)
	unlike := f.d.next(t, "like -far-2")
	f.s.ApplyLikes(api.Likes{
		Added:   []string{"far-2", "midnight-arcade"},
		Removed: []string{"far-1"},
	})
	if f.s.IsLiked("far-2") || !f.s.IsLiked("midnight-arcade") || f.s.IsLiked("far-1") {
		t.Error("reported likes not applied, or applied over a pending one")
	}
	if got := f.s.likeOrder; !slices.Equal(got, []string{"midnight-arcade", "far-2", "far-1"}) {
		t.Errorf("likes %q, want newest first", got)
	}
	unlike.done(nil)
	if f.s.IsLiked("far-2") || len(f.s.TakeNotices()) != 0 {
		t.Error("the unlike that went through was undone")
	}

	// Another front-end's like brings what it knew of the song.
	far3 := catalog.Song{ID: "far-3", Title: "Signal Lost"}
	f.s.ApplyLikes(api.Likes{Added: []string{"far-3"}, Songs: []catalog.Song{far3}})
	if got := f.s.LikedSongs(); len(got) == 0 || got[0] != far3 {
		t.Errorf("liked songs %+v, want %+v first", got, far3)
	}
}

func TestPendingLikeOutlivesLikedMusic(t *testing.T) {
	f := newFixture(t)
	f.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true})
	library, liked := f.takeFetches(t)
	library.Reply <- catalogtest.Reply{}
	f.run(t)
	// A like sent meanwhile is newer than the list.
	f.s.SetLiked("midnight-arcade", true)
	like := f.d.next(t, "like +midnight-arcade")
	liked.Reply <- adaLiked()
	f.run(t)
	if !f.s.IsLiked("far-1") || !f.s.IsLiked("far-2") || !f.s.IsLiked("midnight-arcade") {
		t.Fatal("likes not seeded from Liked music")
	}
	if !slices.Equal(f.s.likeOrder, []string{"midnight-arcade", "far-2", "far-1"}) {
		t.Errorf("likes %q, want newest first", f.s.likeOrder)
	}
	like.done(nil)
	if !f.s.IsLiked("midnight-arcade") {
		t.Error("a like that went through was undone")
	}
}

// Likes ytmd tells went through while Liked music is fetched are newer
// than the list, which may or may not have them.
func TestLikesToldWhileFetching(t *testing.T) {
	f := signedIn(t)
	f.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true})
	library, liked := f.takeFetches(t)
	library.Reply <- catalogtest.Reply{Library: adaLibrary()}
	f.run(t)
	night := catalog.Song{ID: "night", Title: "Night Shift"}
	f.s.ApplyLikes(api.Likes{Added: []string{"night"}, Songs: []catalog.Song{night}})
	f.s.ApplyLikes(api.Likes{Removed: []string{"far-1"}})
	f.s.ApplyLikes(api.Likes{Added: []string{"far-1"}})
	f.s.ApplyLikes(api.Likes{Removed: []string{"far-2"}})
	if !f.s.IsLiked("night") || f.s.IsLiked("far-2") {
		t.Error("told likes wait for the fetch")
	}
	liked.Reply <- adaLiked()
	f.run(t)
	if !f.s.IsLiked("night") || !f.s.IsLiked("far-1") || f.s.IsLiked("far-2") {
		t.Errorf("likes %q, the told ones lost", f.s.likeOrder)
	}
	if got := f.s.LikedSongs(); len(got) != 2 || got[0] != night {
		t.Errorf("liked songs %+v, want %+v first", got, night)
	}
	// Told once the fetch is in, they apply as they come.
	f.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true})
	f.fetched(t)
	f.s.ApplyLikes(api.Likes{Removed: []string{"far-1"}})
	if f.s.IsLiked("far-1") || !f.s.IsLiked("far-2") {
		t.Errorf("likes %q", f.s.likeOrder)
	}
}

// A fetch the account changed since gives nothing.
func TestStaleFetchDropped(t *testing.T) {
	f := newFixture(t)
	f.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true})
	library, liked := f.takeFetches(t)
	f.s.ApplyAccount(api.AccountEvent{Account: grace, SignedIn: true, Switched: true})
	if library.Ctx.Err() == nil || liked.Ctx.Err() == nil {
		t.Error("switching accounts left Ada's fetches running")
	}
	graceLibrary, graceLiked := f.takeFetches(t)
	library.Reply <- catalogtest.Reply{Library: adaLibrary()}
	liked.Reply <- adaLiked()
	graceLibrary.Reply <- catalogtest.Reply{}
	graceLiked.Reply <- catalogtest.Reply{Err: catalog.ErrNotFound}
	f.run(t)
	f.run(t)
	select {
	case <-f.q:
		t.Error("Ada's fetches came back")
	case <-time.After(30 * time.Millisecond):
	}
	if len(f.cat.LibraryPlaylists()) != 0 || f.s.IsLiked("far-1") || f.s.IsBusy() {
		t.Error("Ada's library or likes shown as Grace's")
	}
}

func TestSignIn(t *testing.T) {
	f := signedOut(t)
	// Nothing pasted sends nothing.
	f.s.SignIn(" \n\t")
	f.d.take(t)
	// The daemon cleans what is pasted.
	f.s.SignIn("Cookie: SID=bad;\n HSID=worse")
	signIn := f.d.next(t, "signin Cookie: SID=bad;\n HSID=worse")
	if !f.s.IsSigningIn() || !f.s.IsBusy() {
		t.Error("not signing in")
	}
	signIn.signedIn(catalog.Account{}, errors.New("these cookies are signed out"))
	wantNotices(t, f, notice.Notice{
		Kind: notice.KindSignInError, Title: notice.SignInFailed, Detail: notice.Reason,
		Args: []string{"these cookies are signed out"},
	})
	if f.s.IsSignedIn() || f.s.IsSigningIn() || f.changed() {
		t.Error("a failed sign-in signed in")
	}

	// The daemon tells the account switched before it answers.
	f.s.SignIn("SID=good")
	signIn = f.d.next(t, "signin SID=good")
	f.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true, Switched: true})
	signIn.signedIn(ada, nil)
	wantNotices(t, f, notice.Notice{Kind: notice.KindSignedIn, Title: notice.SignedInAs, Args: []string{"Ada Lovelace"}})
	if !f.s.IsSignedIn() || f.s.Account() != ada || !f.changed() || f.s.IsSigningIn() {
		t.Error("not signed in as Ada")
	}
	// Everything the account has is fetched anew.
	wantState(t, f, AccountRes, fetchstate.Loaded)
	wantState(t, f, LibraryRes, fetchstate.Loading)
	wantState(t, f, LikedRes, fetchstate.Loading)
	wantState(t, f, loader.HomeRes, fetchstate.Idle)
	f.d.take(t)
	f.fetched(t)
	if len(f.cat.LibraryPlaylists()) != 1 || !f.s.IsLiked("far-1") || f.s.IsBusy() {
		t.Error("Ada's library or likes missing")
	}
}

func TestCancelSignIn(t *testing.T) {
	f := signedOut(t)
	f.s.CancelSignIn()
	if f.d.canceled != 0 {
		t.Error("canceled a sign-in not in flight")
	}
	f.s.SignIn("SID=first")
	first := f.d.next(t, "signin SID=first")
	f.s.CancelSignIn()
	if f.s.IsSigningIn() || f.d.canceled != 1 {
		t.Fatal("the sign-in goes on")
	}
	first.signedIn(catalog.Account{}, errors.New("canceled"))
	wantNotices(t, f)

	// A later sign-in decides; an earlier one that went through anyway
	// is told, as it signed in.
	f.s.SignIn("SID=second")
	second := f.d.next(t, "signin SID=second")
	f.s.SignIn("SID=third")
	third := f.d.next(t, "signin SID=third")
	second.signedIn(catalog.Account{}, errors.New("superseded"))
	if !f.s.IsSigningIn() {
		t.Error("an earlier sign-in's answer ended the latest")
	}
	wantNotices(t, f)
	f.s.CancelSignIn()
	third.signedIn(ada, nil)
	wantNotices(t, f, notice.Notice{Kind: notice.KindSignedIn, Title: notice.SignedInAs, Args: []string{"Ada Lovelace"}})
}

func TestSignOut(t *testing.T) {
	f := signedIn(t)
	f.s.SignIn("SID=grace")
	f.d.next(t, "signin SID=grace")
	f.s.SignOut()
	signOut := f.d.next(t, "signout")
	// Until the credentials are gone Ada stays signed in, and a second
	// SignOut asks nothing.
	f.s.SignOut()
	f.d.take(t)
	if !f.s.IsSignedIn() || !f.s.IsSigningOut() {
		t.Error("signed out before the daemon did")
	}
	// The daemon tells no one is signed in, then answers.
	f.s.ApplyAccount(api.AccountEvent{})
	signOut.done(nil)
	if f.s.IsSignedIn() || f.s.IsSigningOut() || f.s.DisplayName() != "" || f.s.IsLiked("far-2") ||
		len(f.cat.LibraryPlaylists()) != 0 || len(f.s.Library().Songs) != 0 || f.s.LikedCount() != 0 {
		t.Error("signing out kept the account, its likes or its library")
	}
	// The daemon dropped the sign-in in flight.
	if f.s.IsSigningIn() || f.s.IsBusy() || f.d.canceled != 1 {
		t.Error("the sign-in in flight outlived the sign-out")
	}
	for _, res := range Resources {
		wantState(t, f, res, fetchstate.SignedOut)
	}
	wantState(t, f, loader.HomeRes, fetchstate.Idle)
	wantNotices(t, f, notice.Notice{Kind: notice.KindInfo, Title: notice.SignedOut})
}

func TestSignOutFails(t *testing.T) {
	f := signedIn(t)
	f.s.SignOut()
	f.d.next(t, "signout").done(errors.New("keychain locked"))
	if !f.s.IsSignedIn() || len(f.cat.LibraryPlaylists()) != 1 || f.changed() || f.s.IsSigningOut() {
		t.Error("a failed sign-out signed out")
	}
	wantNotices(t, f, notice.Notice{
		Kind: notice.KindFailure, Title: notice.SignOutFailed, Detail: notice.Reason,
		Args: []string{"keychain locked"},
	})
}

// A like in flight as the account changes must not change the next one.
func TestStaleLikeRejected(t *testing.T) {
	f := signedIn(t)
	f.s.SetLiked("far-1", false)
	like := f.d.next(t, "like -far-1")
	f.s.ApplyAccount(api.AccountEvent{})
	like.done(errors.New("signed out meanwhile"))
	if f.s.IsLiked("far-1") || len(f.s.TakeNotices()) != 0 || f.s.IsBusy() {
		t.Fatal("a stale like changed the signed-out session")
	}

	f.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true, Switched: true})
	f.s.SetLiked("far-2", true)
	like = f.d.next(t, "like +far-2")
	f.s.ApplyAccount(api.AccountEvent{Account: grace, SignedIn: true, Switched: true})
	like.done(errors.New("Ada's"))
	if f.s.IsLiked("far-2") || len(f.s.TakeNotices()) != 0 {
		t.Error("Ada's like changed Grace's likes")
	}
}

func TestRetryFailed(t *testing.T) {
	f := newFixture(t)
	f.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true})
	library, liked := f.takeFetches(t)
	library.Reply <- catalogtest.Reply{Err: errors.New("offline")}
	liked.Reply <- catalogtest.Reply{Err: errors.New("offline")}
	f.run(t)
	f.run(t)
	// Only what failed is fetched again.
	f.s.RetryFailed()
	wantState(t, f, LibraryRes, fetchstate.Loading)
	wantState(t, f, LikedRes, fetchstate.Loading)
	f.fetched(t)
	wantState(t, f, LibraryRes, fetchstate.Loaded)
	f.s.RetryFailed()
	f.none(t)
	f.d.take(t)
}
