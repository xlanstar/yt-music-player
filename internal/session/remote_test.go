package session

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/catalog/catalogtest"
	"yt-music-player/internal/daemon"
	"yt-music-player/internal/fetchstate"
	"yt-music-player/internal/loader"
	"yt-music-player/internal/notice"
	"yt-music-player/internal/remote"
)

var (
	_ Daemon = (*remote.Account)(nil)
	_ Source = (*remote.Client)(nil)
)

// pipeDialer connects to an in-process Service over net.Pipe.
type pipeDialer struct{ svc *daemon.Service }

func (d pipeDialer) Dial(context.Context) (net.Conn, error) {
	ours, theirs := net.Pipe()
	go d.svc.Serve(theirs) //nolint:contextcheck // the Service's end outlives the dial, as a socket's does
	return ours, nil
}

func (d pipeDialer) Wait(ctx context.Context) (net.Conn, error)        { return d.Dial(ctx) }
func (d pipeDialer) DialOrSpawn(ctx context.Context) (net.Conn, error) { return d.Dial(ctx) }

// remoteFixture is a Session over a Client and its remote.Account,
// connected to a Service over catalogtest, which the test answers; the
// test's goroutine is the UI thread.
type remoteFixture struct {
	src catalogtest.Accounts
	svc *daemon.Service
	*frontEnd
}

// frontEnd is a Session over a Client and its remote.Account, as a
// front-end has it.
type frontEnd struct {
	s       *Session
	c       *remote.Client
	fetches fetchstate.Registry
	cat     *catalog.Catalog
	q       chan func()
}

func newRemoteFixture(t *testing.T) *remoteFixture {
	t.Helper()
	f := &remoteFixture{src: catalogtest.Accounts{Source: catalogtest.New(t)}}
	f.svc = daemon.New(daemon.Options{Source: f.src})
	f.svc.Start()
	t.Cleanup(f.svc.Shutdown)
	f.frontEnd = f.connect(t)
	return f
}

// connect connects another front-end to the Service.
func (f *remoteFixture) connect(t *testing.T) *frontEnd {
	t.Helper()
	fe := &frontEnd{cat: catalog.New(nil, nil, nil), q: make(chan func(), 64)}
	post := func(fn func()) { fe.q <- fn }
	c := remote.NewClient(post, remote.Handler{})
	fe.c = c
	load := loader.New(c, &fe.fetches, fe.cat, post)
	fe.s = New(Options{
		Daemon: remote.NewAccount(c), Source: c,
		Post: post, Fetches: &fe.fetches, Loader: load, Catalog: fe.cat,
	})
	c.SetAccountEvents(fe.s)
	sup := remote.Supervise(c, remote.Options{
		Role: api.RoleDesktop, Version: "1.2.3", Dialer: pipeDialer{f.svc},
		Spawn: func() error { return errors.New("no ytmd to start") },
	})
	// Before the Service's Shutdown, registered earlier.
	t.Cleanup(sup.Close)
	return fe
}

// until runs what is posted until cond holds.
func (f *frontEnd) until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for !cond() {
		select {
		case fn := <-f.q:
			fn()
		case <-deadline:
			t.Fatalf("never %s", what)
		}
	}
}

// runPosted runs what is posted by now.
func (f *frontEnd) runPosted() {
	for {
		select {
		case fn := <-f.q:
			fn()
		default:
			return
		}
	}
}

func (f *remoteFixture) next(t *testing.T, kind, id string) catalogtest.Call {
	t.Helper()
	return f.src.Next(t, nil, kind, id)
}

// fetches returns fe's fetches of the library and Liked music, running
// its posts meanwhile: fe fetches once it hears who is signed in.
func (f *remoteFixture) takeFetches(t *testing.T, fe *frontEnd) (library, liked catalogtest.Call) {
	t.Helper()
	calls := f.src.Take(t, fe.runPosted, "library", "playlist LM")
	return calls["library"], calls["playlist LM"]
}

// fetched answers fe's fetches with Ada's library and ids liked, songs
// describing them, and waits until fe has them.
func (f *remoteFixture) fetched(t *testing.T, fe *frontEnd, ids []string, songs []catalog.Song) {
	t.Helper()
	library, liked := f.takeFetches(t, fe)
	library.Reply <- catalogtest.Reply{Library: adaLibrary()}
	liked.Reply <- catalogtest.Reply{Playlist: catalog.Playlist{ID: catalog.LikedID, Songs: ids}, Songs: songs}
	fe.until(t, "fetched Ada's account", func() bool {
		for _, res := range Resources {
			if fe.fetches.Get(res).State != fetchstate.Loaded {
				return false
			}
		}
		return fe.s.IsSignedIn() && !fe.s.IsBusy()
	})
}

var adaLikedIDs = []string{"far-2", "far-1"}

func adaSongs() []catalog.Song { return []catalog.Song{tracks[1], tracks[0]} }

// signedIn answers the daemon's first fetch as Ada's, then the
// front-end's.
func (f *remoteFixture) signedIn(t *testing.T) {
	t.Helper()
	f.signedInLiking(t, adaLikedIDs, adaSongs())
}

// signedInLiking is signedIn with ids liked, songs describing them.
func (f *remoteFixture) signedInLiking(t *testing.T, ids []string, songs []catalog.Song) {
	t.Helper()
	f.next(t, "account", "").Reply <- catalogtest.Reply{Account: ada}
	f.fetched(t, f.frontEnd, ids, songs)
}

// signedOut answers the daemon's first fetch: no one is signed in.
func (f *remoteFixture) signedOut(t *testing.T) {
	t.Helper()
	f.next(t, "account", "").Reply <- catalogtest.Reply{Err: catalog.ErrSignedOut}
	f.until(t, "signed out", func() bool { return f.fetches.Get(AccountRes).State == fetchstate.SignedOut })
}

// Over the socket too, a Like's ErrSignedOut comes before the Account
// event of the sign-out.
func TestRemoteLikeSignedOut(t *testing.T) {
	f := newRemoteFixture(t)
	f.signedIn(t)
	f.s.SetLiked("far-1", false)
	f.next(t, "like", "-far-1").Reply <- catalogtest.Reply{Err: catalog.ErrSignedOut}
	var notices []notice.Notice
	f.until(t, "told to sign in", func() bool {
		notices = append(notices, f.s.TakeNotices()...)
		return len(notices) > 0
	})
	if !reflect.DeepEqual(notices, []notice.Notice{signInToLikeNotice}) {
		t.Errorf("notices %+v", notices)
	}
	f.until(t, "signed out", func() bool { return !f.s.IsSignedIn() })
	if f.s.IsBusy() || f.s.IsLiked("far-2") {
		t.Error("the sign-out left a like")
	}
}

// A sign-in canceled is canceled in the daemon too: the source's answer
// after the cancel signs no one in, unless it stored the credentials
// anyway, as package account promises.
func TestRemoteCancelSignIn(t *testing.T) {
	f := newRemoteFixture(t)
	f.signedOut(t)
	canceled := func(call catalogtest.Call) {
		t.Helper()
		select {
		case <-call.Ctx.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("the daemon left the sign-in canceled running")
		}
	}

	f.s.SignIn("SID=late")
	late := f.next(t, "signin", "SID=late")
	f.s.CancelSignIn()
	canceled(late)
	late.Reply <- catalogtest.Reply{Account: catalog.Account{Name: "Bob"}, Err: late.Ctx.Err()}

	// Stored anyway: the Account event signs in, and nothing is told.
	f.s.SignIn("SID=stored")
	stored := f.next(t, "signin", "SID=stored")
	f.s.CancelSignIn()
	canceled(stored)
	stored.Reply <- catalogtest.Reply{Account: ada}
	var notices []notice.Notice
	f.until(t, "signed in", func() bool {
		notices = append(notices, f.s.TakeNotices()...)
		return f.s.IsSignedIn()
	})
	if f.s.Account() != ada || f.s.IsSigningIn() || len(notices) > 0 {
		t.Errorf("signed in as %+v (signing in %v), notices %+v", f.s.Account(), f.s.IsSigningIn(), notices)
	}
	f.fetched(t, f.frontEnd, adaLikedIDs, adaSongs())
	if notices = f.s.TakeNotices(); f.s.Account() != ada || len(notices) > 0 {
		t.Errorf("signed in as %+v, notices %+v", f.s.Account(), notices)
	}
}

func TestRemoteSignIn(t *testing.T) {
	f := newRemoteFixture(t)
	f.signedOut(t)

	// A sign-in canceled says nothing, though the daemon answers.
	f.s.SignIn("SID=first")
	first := f.next(t, "signin", "SID=first")
	f.s.CancelSignIn()
	first.Reply <- catalogtest.Reply{Err: errors.New("these cookies are signed out")}

	f.s.SignIn("SID=good")
	f.next(t, "signin", "SID=good").Reply <- catalogtest.Reply{Account: ada}
	var notices []notice.Notice
	f.until(t, "signed in", func() bool {
		notices = append(notices, f.s.TakeNotices()...)
		return !f.s.IsSigningIn()
	})
	want := []notice.Notice{{Kind: notice.KindSignedIn, Title: notice.SignedInAs, Args: []string{"Ada Lovelace"}}}
	if !reflect.DeepEqual(notices, want) {
		t.Errorf("notices %+v, want %+v", notices, want)
	}
	if !f.s.IsSignedIn() || f.fetches.Get(LibraryRes).State != fetchstate.Loading {
		t.Error("not signed in, or the library not fetched anew")
	}

	// The fetches go through the daemon, whose errors say nothing of
	// themselves; Retry fetches again.
	library, liked := f.takeFetches(t, f.frontEnd)
	library.Reply <- catalogtest.Reply{Err: errors.New("network is down")}
	liked.Reply <- catalogtest.Reply{Err: catalog.ErrNotFound}
	f.until(t, "failed", func() bool { return f.fetches.Get(LibraryRes).State == fetchstate.Failed })
	if err := f.fetches.Get(LibraryRes).Err; !errors.Is(err, api.ErrInternal) {
		t.Errorf("library failed with %v", err)
	}
	f.s.Retry(LibraryRes)
	f.next(t, "library", "").Reply <- catalogtest.Reply{Library: adaLibrary()}
	f.until(t, "fetched", func() bool { return f.fetches.Get(LibraryRes).State == fetchstate.Loaded })
}

// A song liked on one front-end shows in the others' Liked music, those
// connected then and those fetching it, though their catalogs never had it.
func TestRemoteLikeDescribed(t *testing.T) {
	f := newRemoteFixture(t)
	f.signedIn(t)
	other := f.connect(t)
	f.fetched(t, other, adaLikedIDs, adaSongs())
	late := f.connect(t)
	library, liked := f.takeFetches(t, late)

	song := catalog.Song{ID: "far-9", Title: "Night Shift", Artist: "Far"}
	f.cat.Add([]catalog.Song{song}, nil, nil)
	f.s.SetLiked(song.ID, true)
	f.next(t, "like", "+far-9").Reply <- catalogtest.Reply{}
	f.until(t, "liked", func() bool { return !f.s.IsBusy() })
	shown := func(fe *frontEnd) func() bool {
		return func() bool {
			liked := fe.s.LikedSongs()
			return len(liked) > 0 && liked[0] == song
		}
	}
	other.until(t, "shown the song liked", shown(other))
	// Liked music fetched before the like went through still shows it.
	late.until(t, "shown the song liked", shown(late))
	library.Reply <- catalogtest.Reply{Library: adaLibrary()}
	liked.Reply <- catalogtest.Reply{Playlist: catalog.Playlist{ID: catalog.LikedID, Songs: adaLikedIDs}, Songs: adaSongs()}
	late.until(t, "fetched", func() bool { return !late.s.IsBusy() })
	if !shown(late)() || late.s.LikedCount() != 3 {
		t.Errorf("liked songs %+v after the fetch", late.s.LikedSongs())
	}
}

// A fetch answered ErrSignedOut signs ytmd out, and every front-end.
func TestRemoteFetchSignedOut(t *testing.T) {
	f := newRemoteFixture(t)
	f.signedIn(t)
	other := f.connect(t)
	f.fetched(t, other, adaLikedIDs, adaSongs())
	f.s.Retry(LikedRes)
	f.next(t, "playlist", catalog.LikedID).Reply <- catalogtest.Reply{Err: catalog.ErrSignedOut}
	for _, fe := range []*frontEnd{f.frontEnd, other} {
		fe.until(t, "signed out", func() bool { return !fe.s.IsSignedIn() })
		for _, res := range Resources {
			if got := fe.fetches.Get(res).State; got != fetchstate.SignedOut {
				t.Errorf("%+v is %d, want signed out", res, got)
			}
		}
		if fe.s.LikedCount() != 0 {
			t.Error("signed out, the likes stayed")
		}
	}
}

// Lists too long for one frame come in parts (daemon's carry, and a
// reply's api.Ahead): front-ends connected then and later end with every
// song.
func TestRemoteLongLists(t *testing.T) {
	f := newRemoteFixture(t)
	liked := catalogtest.Decoded(1000)
	f.signedInLiking(t, ids(liked), liked)
	queue := liked[:600]
	f.c.PlayAll(ids(queue), queue)
	f.until(t, "got the queue", func() bool { return len(f.c.Mirror().Queue()) == len(queue) })
	late := f.connect(t)
	f.fetched(t, late, ids(liked), liked)
	late.until(t, "got the queue", func() bool { return len(late.c.Mirror().Queue()) == len(queue) })
	for _, fe := range []*frontEnd{f.frontEnd, late} {
		if got := fe.s.LikedSongs(); !reflect.DeepEqual(got, liked) {
			t.Errorf("%d liked songs, want %d", len(got), len(liked))
		}
		m := fe.c.Mirror()
		for i, id := range m.Queue() {
			if song, ok := m.Song(id); !ok || song != queue[i] {
				t.Errorf("queued song %d is %+v, want %+v", i, song, queue[i])
			}
		}
	}
}

func ids(songs []catalog.Song) []string {
	out := make([]string, len(songs))
	for i, song := range songs {
		out[i] = song.ID
	}
	return out
}
