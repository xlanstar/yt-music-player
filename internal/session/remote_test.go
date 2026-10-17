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

// pipeDialer connects to an in-process Service over net.Pipe.
type pipeDialer struct{ svc *daemon.Service }

func (d pipeDialer) Dial(context.Context) (net.Conn, error) {
	ours, theirs := net.Pipe()
	go d.svc.Serve(theirs) //nolint:contextcheck // the Service's end outlives the dial, as a socket's does
	return ours, nil
}

func (d pipeDialer) Wait(ctx context.Context) (net.Conn, error)        { return d.Dial(ctx) }
func (d pipeDialer) DialOrSpawn(ctx context.Context) (net.Conn, error) { return d.Dial(ctx) }

// remoteFixture is a Session over Remote, over a Client connected to a
// Service over catalogtest, which the test answers; the test's goroutine
// is the UI thread.
type remoteFixture struct {
	src catalogtest.Accounts
	svc *daemon.Service
	*frontEnd
}

// frontEnd is a Session over Remote, as a front-end has it.
type frontEnd struct {
	s      *Session
	states fetchstate.Registry
	cat    *catalog.Catalog
	q      chan func()
}

func newRemoteFixture(t *testing.T) *remoteFixture {
	t.Helper()
	f := &remoteFixture{src: catalogtest.Accounts{Source: catalogtest.New()}}
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
	c := remote.NewClient(post, remote.Handler{
		Account: func(e api.AccountEvent) { fe.s.ApplyAccount(e) },
		Library: func(l catalog.Library) { fe.s.ApplyLibrary(l) },
		Likes:   func(l api.Likes) { fe.s.ApplyLikes(l) },
		Failed:  func(e api.FailedEvent) { fe.s.ApplyFailed(e) },
	})
	load := loader.New(c, &fe.states, fe.cat, post)
	fe.s = New(NewRemote(c), &fe.states, load, fe.cat)
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

func (f *remoteFixture) next(t *testing.T, kind, id string) catalogtest.Call {
	t.Helper()
	return f.src.Next(t, nil, kind, id)
}

// signedIn answers the daemon's first fetches as Ada's.
func (f *remoteFixture) signedIn(t *testing.T) {
	t.Helper()
	calls := f.src.Take(t, nil, "account", "library", "playlist LM")
	calls["account"].Reply <- catalogtest.Reply{Account: ada}
	calls["library"].Reply <- catalogtest.Reply{Library: adaLibrary()}
	calls["playlist LM"].Reply <- catalogtest.Reply{
		Playlist: catalog.Playlist{ID: catalog.LikedID, Songs: adaLikes.IDs}, Songs: adaLikes.Songs,
	}
	f.until(t, "fetched Ada's account", func() bool {
		for _, res := range Resources {
			if f.states.State(res).State != fetchstate.Loaded {
				return false
			}
		}
		return f.s.IsSignedIn()
	})
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
	if f.s.IsBusy() || f.s.Liked("far-2") {
		t.Error("the sign-out left a like")
	}
}

// A sign-in canceled is canceled in the daemon too: the source's answer
// after the cancel signs no one in, unless it stored the credentials
// anyway, as package account promises.
func TestRemoteCancelSignIn(t *testing.T) {
	f := newRemoteFixture(t)
	calls := f.src.Take(t, nil, "account", "library", "playlist LM")
	for _, call := range calls {
		call.Reply <- catalogtest.Reply{Err: catalog.ErrSignedOut}
	}
	f.until(t, "signed out", func() bool { return f.states.State(AccountRes).State == fetchstate.SignedOut })
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
	f.signedIn(t)
	if notices = f.s.TakeNotices(); f.s.Account() != ada || len(notices) > 0 {
		t.Errorf("signed in as %+v, notices %+v", f.s.Account(), notices)
	}
}

func TestRemoteSignIn(t *testing.T) {
	f := newRemoteFixture(t)
	calls := f.src.Take(t, nil, "account", "library", "playlist LM")
	for _, call := range calls {
		call.Reply <- catalogtest.Reply{Err: catalog.ErrSignedOut}
	}
	f.until(t, "signed out", func() bool { return f.states.State(AccountRes).State == fetchstate.SignedOut })

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
	want := []notice.Notice{{Kind: notice.SignedIn, Title: notice.SignedInAs, Args: []string{"Ada Lovelace"}}}
	if !reflect.DeepEqual(notices, want) {
		t.Errorf("notices %+v, want %+v", notices, want)
	}
	if !f.s.IsSignedIn() || f.states.State(LibraryRes).State != fetchstate.Loading {
		t.Error("not signed in, or the library not fetched anew")
	}

	// Retry asks the daemon, which fetches.
	f.signedIn(t)
	f.s.Retry(LibraryRes)
	f.next(t, "library", "").Reply <- catalogtest.Reply{Err: errors.New("network is down")}
	f.until(t, "failed", func() bool { return f.states.State(LibraryRes).State == fetchstate.Failed })
	if err := f.states.State(LibraryRes).Err; !errors.Is(err, api.ErrInternal) {
		t.Errorf("library failed with %v", err)
	}
}

// A song liked on one front-end shows in the others' Liked music, those
// connected then and later, though their catalogs never had it.
func TestRemoteLikeDescribed(t *testing.T) {
	f := newRemoteFixture(t)
	f.signedIn(t)
	other := f.connect(t)
	other.until(t, "got the likes", func() bool { return other.s.LikedCount() == len(adaLikes.IDs) })

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
	late := f.connect(t)
	late.until(t, "shown the song liked", shown(late))
}
