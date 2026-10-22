package session

import (
	"errors"
	"reflect"
	"slices"
	"testing"
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
	// what is "refresh account", "refresh library" or "refresh liked",
	// "like +id" or "like -id", "signin <credentials>" or "signout".
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

var partNames = map[api.Part]string{api.PartAccount: "account", api.PartLibrary: "library", api.PartLiked: "liked"}

func (d *fakeDaemon) Refresh(p api.Part, done func(error)) {
	d.requests = append(d.requests, request{what: "refresh " + partNames[p], done: done})
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

// fixture is a Session over a fakeDaemon, as the app starts: ytmd fetches
// the account's resources and sends them.
type fixture struct {
	d       *fakeDaemon
	s       *Session
	fetches fetchstate.Registry
	cat     *catalog.Catalog
}

func newFixture(tb testing.TB) *fixture {
	f := &fixture{d: &fakeDaemon{}, cat: catalog.New(nil, nil, nil)}
	// The loader only forgets Home here: it never runs its post.
	load := loader.New(catalogtest.New(tb), &f.fetches, f.cat, func(func()) { panic("the loader fetched") })
	f.s = New(f.d, &f.fetches, load, f.cat)
	// Loaded so changed can see an account change forget it.
	f.fetches.Set(loader.HomeFeed, fetchstate.Fetch{State: fetchstate.Loaded})
	for _, res := range Resources {
		f.fetches.Set(res, fetchstate.Fetch{State: fetchstate.Loading})
	}
	return f
}

// changed reports whether an account change forgot Home.
func (f *fixture) changed() bool { return f.fetches.Get(loader.HomeFeed).State == fetchstate.Idle }

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
	ada      = catalog.Account{Name: "Ada Lovelace", Handle: "@ada"}
	adaMix   = catalog.Playlist{ID: "ada-mix", Name: "Engine Notes", Songs: []string{"far-1", "far-2"}}
	tracks   = []catalog.Song{{ID: "far-1", Title: "Carrier Wave"}, {ID: "far-2", Title: "Dead Air"}}
	adaLikes = api.Likes{Full: true, IDs: []string{"far-2", "far-1"}, Songs: []catalog.Song{tracks[1], tracks[0]}}

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

// fetched gives s Ada's account, library and likes as the daemon's events.
func fetched(s *Session) {
	s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true})
	s.ApplyLibrary(adaLibrary())
	s.ApplyLikes(adaLikes)
}

func signedIn(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	fetched(f.s)
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
	if !f.s.IsBusy() {
		t.Error("not busy while fetching")
	}
	fetched(f.s)
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
	if !f.s.Liked("far-1") || !f.s.Liked("far-2") || f.s.LikedCount() != 2 {
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

// seedLikes must not alias its argument.
func TestLiked(t *testing.T) {
	s := newFixture(t).s
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

func TestSignedOutEverywhere(t *testing.T) {
	f := signedOut(t)
	for _, res := range Resources {
		wantState(t, f, res, fetchstate.SignedOut)
	}
	if f.s.IsBusy() {
		t.Error("busy once signed out")
	}
	// No one was signed in, so Home stays.
	wantState(t, f, loader.HomeFeed, fetchstate.Loaded)

	// Liking asks to sign in, and asks the daemon nothing.
	f.s.SetLiked("far-1", true)
	if f.s.Liked("far-1") {
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
	if f.s.IsSignedIn() || f.s.Liked("far-2") || len(f.cat.LibraryPlaylists()) != 0 || len(f.s.Library().Songs) != 0 {
		t.Error("signed out, the account's library or likes stayed")
	}
	wantState(t, f, LibraryRes, fetchstate.SignedOut)
	// Home was Ada's, so it is refetched.
	wantState(t, f, loader.HomeFeed, fetchstate.Idle)
	wantNotices(t, f)
}

func TestLibraryFailsAndRetries(t *testing.T) {
	f := newFixture(t)
	f.s.ApplyFailed(api.FailedEvent{Part: api.PartAccount, Err: &api.Error{Code: api.CodeInternal}})
	f.s.ApplyFailed(api.FailedEvent{Part: api.PartLibrary, Err: &api.Error{Code: api.CodeInternal, Detail: "network is down"}})
	f.s.ApplyLikes(adaLikes)
	wantState(t, f, LibraryRes, fetchstate.Failed)
	err := f.fetches.Get(LibraryRes).Err
	if !errors.Is(err, api.ErrInternal) || err.Error() != "api: ytmd failed: network is down" {
		t.Errorf("error %v", err)
	}
	if f.s.IsBusy() {
		t.Error("busy once every fetch ended")
	}
	f.d.take(t)
	// Retrying retries the failed account too.
	f.s.Retry(LibraryRes)
	wantState(t, f, LibraryRes, fetchstate.Loading)
	f.d.take(t, "refresh library", "refresh account")
	f.s.ApplyLibrary(adaLibrary())
	f.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true})
	wantState(t, f, LibraryRes, fetchstate.Loaded)
	wantState(t, f, AccountRes, fetchstate.Loaded)
	f.s.Retry(LikedRes)
	f.d.take(t, "refresh liked")
}

func TestRefreshFails(t *testing.T) {
	f := signedIn(t)
	f.s.Retry(LibraryRes)
	f.d.next(t, "refresh library").done(api.ErrDisconnected)
	if err := f.fetches.Get(LibraryRes).Err; !errors.Is(err, api.ErrDisconnected) {
		t.Errorf("library %v, want the request's failure", err)
	}
	// An answer after the event changes nothing.
	f.s.Retry(LikedRes)
	refresh := f.d.next(t, "refresh liked")
	f.s.ApplyLikes(adaLikes)
	refresh.done(api.ErrDisconnected)
	wantState(t, f, LikedRes, fetchstate.Loaded)
	refresh.done(nil)
	wantState(t, f, LikedRes, fetchstate.Loaded)
}

func TestFailedEventChecked(t *testing.T) {
	f := signedIn(t)
	f.s.ApplyFailed(api.FailedEvent{Part: 9, Err: &api.Error{Code: api.CodeInternal}})
	for _, res := range Resources {
		wantState(t, f, res, fetchstate.Loaded)
	}
	f.s.ApplyFailed(api.FailedEvent{Part: api.PartLiked})
	if err := f.fetches.Get(LikedRes).Err; !errors.Is(err, api.ErrInternal) {
		t.Errorf("a failure without its error is %v", err)
	}
}

func TestLikeAppliesAtOnceAndReverts(t *testing.T) {
	f := signedIn(t)
	gen := f.s.LikesGen()
	f.s.SetLiked("far-2", false)
	// Unliking takes the song off at once, before the daemon answers.
	if f.s.Liked("far-2") || f.s.LikedCount() != 1 || f.s.LikesGen() == gen || !f.s.IsBusy() {
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
	if !f.s.Liked("far-2") || f.s.LikesGen() == gen || f.s.IsBusy() {
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
	if !f.s.Liked("far-1") {
		t.Error("an outdated like's failure reverted the later one")
	}
	second.done(nil)
	if !f.s.Liked("far-1") || len(f.s.TakeNotices()) != 0 || f.s.IsBusy() {
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
	if f.s.Liked("far-2") || !f.s.Liked("midnight-arcade") || f.s.Liked("far-1") {
		t.Error("reported likes not applied, or applied over a pending one")
	}
	if got := f.s.likeOrder; !slices.Equal(got, []string{"midnight-arcade", "far-2", "far-1"}) {
		t.Errorf("likes %q, want newest first", got)
	}
	unlike.done(nil)
	if f.s.Liked("far-2") || len(f.s.TakeNotices()) != 0 {
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
	// A like sent meanwhile is newer than the list.
	f.s.SetLiked("midnight-arcade", true)
	like := f.d.next(t, "like +midnight-arcade")
	f.s.ApplyLikes(adaLikes)
	if !f.s.Liked("far-1") || !f.s.Liked("far-2") || !f.s.Liked("midnight-arcade") {
		t.Fatal("likes not seeded from Liked music")
	}
	if !slices.Equal(f.s.likeOrder, []string{"midnight-arcade", "far-2", "far-1"}) {
		t.Errorf("likes %q, want newest first", f.s.likeOrder)
	}
	like.done(nil)
	if !f.s.Liked("midnight-arcade") {
		t.Error("a like that went through was undone")
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
	// Everything the account has is being fetched anew, by the daemon.
	for _, res := range Resources {
		wantState(t, f, res, fetchstate.Loading)
	}
	wantState(t, f, loader.HomeFeed, fetchstate.Idle)
	f.d.take(t)
	fetched(f.s)
	if len(f.cat.LibraryPlaylists()) != 1 || !f.s.Liked("far-1") || f.s.IsBusy() {
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
	if f.s.IsSignedIn() || f.s.IsSigningOut() || f.s.DisplayName() != "" || f.s.Liked("far-2") ||
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
	wantState(t, f, loader.HomeFeed, fetchstate.Idle)
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
	if f.s.Liked("far-1") || len(f.s.TakeNotices()) != 0 || f.s.IsBusy() {
		t.Fatal("a stale like changed the signed-out session")
	}

	f.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true, Switched: true})
	f.s.SetLiked("far-2", true)
	like = f.d.next(t, "like +far-2")
	f.s.ApplyAccount(api.AccountEvent{Account: catalog.Account{Name: "Grace Hopper"}, SignedIn: true, Switched: true})
	like.done(errors.New("Ada's"))
	if f.s.Liked("far-2") || len(f.s.TakeNotices()) != 0 {
		t.Error("Ada's like changed Grace's likes")
	}
}

func TestRetryFailed(t *testing.T) {
	f := newFixture(t)
	f.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true})
	f.s.ApplyFailed(api.FailedEvent{Part: api.PartLibrary, Err: &api.Error{Code: api.CodeInternal}})
	f.s.ApplyFailed(api.FailedEvent{Part: api.PartLiked, Err: &api.Error{Code: api.CodeInternal}})
	// Only what failed is fetched again.
	f.s.RetryFailed()
	wantState(t, f, LibraryRes, fetchstate.Loading)
	wantState(t, f, LikedRes, fetchstate.Loading)
	f.d.take(t, "refresh library", "refresh liked")
	f.s.ApplyLibrary(adaLibrary())
	f.s.ApplyLikes(adaLikes)
	wantState(t, f, LibraryRes, fetchstate.Loaded)
	f.s.RetryFailed()
	f.d.take(t)
}
