package session

import (
	"context"
	"fmt"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/remote"
)

var errNoAccount = fmt.Errorf("%w: a sign-in's reply without its account", api.ErrInvalid)

var _ Daemon = (*Remote)(nil)

// Remote is the Daemon over a remote.Client, whose Go keeps each answer
// in its place among the events. Use it on the thread the Client posts to.
type Remote struct {
	c            *remote.Client
	cancelSignIn context.CancelFunc // the latest sign-in's
}

// NewRemote returns the Daemon over c, whose Handler must give the
// account's events to the Session's Apply methods.
func NewRemote(c *remote.Client) *Remote { return &Remote{c: c} }

// Refresh implements Daemon.
func (r *Remote) Refresh(part api.Part, done func(error)) {
	r.c.Go(context.Background(), api.Call{Method: api.MethodRefresh, Part: &api.PartArgs{Part: part}},
		func(_ *api.Reply, err error) { done(err) })
}

// Like implements Daemon.
func (r *Remote) Like(id string, liked bool, song *catalog.Song, done func(error)) {
	r.c.Go(context.Background(), api.Call{Method: api.MethodLike, Like: &api.LikeArgs{ID: id, Liked: liked, Song: song}},
		func(_ *api.Reply, err error) { done(err) })
}

// SignIn implements Daemon; the daemon drops a sign-in canceled or
// replaced.
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
