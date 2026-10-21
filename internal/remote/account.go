package remote

import (
	"context"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
)

// Account is what a session asks of ytmd's account, over a Client whose
// Go keeps each answer in its place among the events. Use it on the
// thread the Client posts to.
type Account struct {
	c            *Client
	cancelSignIn context.CancelFunc // the latest sign-in's
}

// NewAccount returns the account over c, whose Handler must give the
// account's events to the session.
func NewAccount(c *Client) *Account { return &Account{c: c} }

// Refresh has who is signed in fetched anew, whose result comes as an
// event; done gets why the request failed, or nil.
func (a *Account) Refresh(done func(error)) {
	a.c.Go(context.Background(), api.Call{Method: api.MethodRefresh}, func(_ *api.Reply, err error) { done(err) })
}

// Like likes or unlikes song id; song, when liked, is what the caller
// knows of it, or nil.
func (a *Account) Like(id string, liked bool, song *catalog.Song, done func(error)) {
	a.c.Go(context.Background(), api.Call{Method: api.MethodLike, Like: &api.LikeArgs{ID: id, Liked: liked, Song: song}},
		func(_ *api.Reply, err error) { done(err) })
}

// SignIn sends credentials as pasted, canceling the sign-in in flight;
// the daemon drops a sign-in canceled or replaced.
func (a *Account) SignIn(credentials string, done func(catalog.Account, error)) {
	a.CancelSignIn()
	ctx, cancel := context.WithCancel(context.Background())
	a.cancelSignIn = cancel
	a.c.Go(ctx, api.Call{Method: api.MethodSignIn, SignIn: &api.SignInArgs{Credentials: credentials}},
		func(reply *api.Reply, err error) {
			cancel()
			done(resultOf(reply, err, accountOf))
		})
}

// CancelSignIn gives up the sign-in in flight.
func (a *Account) CancelSignIn() {
	if a.cancelSignIn != nil {
		a.cancelSignIn()
		a.cancelSignIn = nil
	}
}

// SignOut clears the stored credentials.
func (a *Account) SignOut(done func(error)) {
	a.c.Go(context.Background(), api.Call{Method: api.MethodSignOut}, func(_ *api.Reply, err error) { done(err) })
}
