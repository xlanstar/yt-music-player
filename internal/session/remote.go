package session

import (
	"context"
	"fmt"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/remote"
)

// errNoAccount is a sign-in's reply without its account.
var errNoAccount = fmt.Errorf("%w: a sign-in's reply without its account", api.ErrInvalid)

var _ Daemon = (*Remote)(nil)

// Remote is the Daemon over a remote.Client: every request goes with
// Client.Go, so its answer keeps its place among the events. Use it on the
// thread the Client posts to.
type Remote struct {
	c *remote.Client
	// cancelSignIn cancels the latest sign-in (nothing once it ended),
	// nil before the first.
	cancelSignIn context.CancelFunc
}

// NewRemote returns the Daemon over c. c's Handler must give the account's
// events to the Session's Apply methods:
//
//	remote.Handler{
//		Account: func(e api.AccountEvent) { s.ApplyAccount(e) },
//		Library: func(l catalog.Library) { s.ApplyLibrary(l) },
//		Likes:   func(l api.Likes) { s.ApplyLikes(l) },
//		Failed:  func(e api.FailedEvent) { s.ApplyFailed(e) },
//	}
func NewRemote(c *remote.Client) *Remote { return &Remote{c: c} }

// CanSignIn reports true: the Client is catalog.Accounts, and a daemon
// whose source signs in no one answers SignIn with an error.
func (*Remote) CanSignIn() bool { return true }

// Refresh implements Daemon.
func (r *Remote) Refresh(part api.Part, done func(error)) {
	r.c.Go(context.Background(), api.Call{Method: api.MethodRefresh, Part: &api.PartArgs{Part: part}},
		func(_ *api.Reply, err error) { done(err) })
}

// Like implements Daemon.
func (r *Remote) Like(id string, liked bool, done func(error)) {
	r.c.Go(context.Background(), api.Call{Method: api.MethodLike, Like: &api.LikeArgs{ID: id, Liked: liked}},
		func(_ *api.Reply, err error) { done(err) })
}

// SignIn implements Daemon. A sign-in canceled or replaced answers
// ctx.Err() at once; the daemon is told to drop it.
func (r *Remote) SignIn(credentials string, done func(catalog.Account, error)) {
	r.CancelSignIn()
	ctx, cancel := context.WithCancel(context.Background())
	r.cancelSignIn = cancel
	r.c.Go(ctx, api.Call{Method: api.MethodSignIn, SignIn: &api.SignInArgs{Credentials: credentials}},
		func(reply *api.Reply, err error) {
			cancel()
			switch {
			case err != nil:
				done(catalog.Account{}, err)
			case reply.Account == nil:
				done(catalog.Account{}, errNoAccount)
			default:
				done(*reply.Account, nil)
			}
		})
}

// CancelSignIn implements Daemon.
func (r *Remote) CancelSignIn() {
	if r.cancelSignIn != nil {
		r.cancelSignIn()
		r.cancelSignIn = nil
	}
}

// SignOut implements Daemon.
func (r *Remote) SignOut(done func(error)) {
	r.c.Go(context.Background(), api.Call{Method: api.MethodSignOut}, func(_ *api.Reply, err error) { done(err) })
}
