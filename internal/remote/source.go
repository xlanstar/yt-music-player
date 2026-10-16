package remote

import (
	"context"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
)

var (
	_ catalog.Source   = (*Client)(nil)
	_ catalog.Accounts = (*Client)(nil)
)

// Search implements catalog.Source.
func (c *Client) Search(ctx context.Context, query string) (catalog.Results, error) {
	r, err := c.Call(ctx, api.Call{Method: api.MethodSearch, Query: &api.QueryArgs{Query: query}})
	switch {
	case err != nil:
		return catalog.Results{}, err
	case r.Results == nil:
		return catalog.Results{}, errNoResult
	}
	return *r.Results, nil
}

// Album implements catalog.Source.
func (c *Client) Album(ctx context.Context, id string) (catalog.Album, []catalog.Song, error) {
	r, err := c.Call(ctx, api.Call{Method: api.MethodAlbum, Ref: &api.RefArgs{ID: id}})
	switch {
	case err != nil:
		return catalog.Album{}, nil, err
	case r.Album == nil:
		return catalog.Album{}, nil, errNoResult
	}
	return r.Album.Album, r.Album.Songs, nil
}

// Playlist implements catalog.Source.
func (c *Client) Playlist(ctx context.Context, id string) (catalog.Playlist, []catalog.Song, error) {
	r, err := c.Call(ctx, api.Call{Method: api.MethodPlaylist, Ref: &api.RefArgs{ID: id}})
	switch {
	case err != nil:
		return catalog.Playlist{}, nil, err
	case r.Playlist == nil:
		return catalog.Playlist{}, nil, errNoResult
	}
	return r.Playlist.Playlist, r.Playlist.Songs, nil
}

// Home implements catalog.Source.
func (c *Client) Home(ctx context.Context) (catalog.Home, error) {
	r, err := c.Call(ctx, api.Call{Method: api.MethodHome})
	switch {
	case err != nil:
		return catalog.Home{}, err
	case r.Home == nil:
		return catalog.Home{}, errNoResult
	}
	return *r.Home, nil
}

// Explore implements catalog.Source.
func (c *Client) Explore(ctx context.Context) (catalog.Explore, error) {
	r, err := c.Call(ctx, api.Call{Method: api.MethodExplore})
	switch {
	case err != nil:
		return catalog.Explore{}, err
	case r.Explore == nil:
		return catalog.Explore{}, errNoResult
	}
	return *r.Explore, nil
}

// Account implements catalog.Source.
func (c *Client) Account(ctx context.Context) (catalog.Account, error) {
	r, err := c.Call(ctx, api.Call{Method: api.MethodAccount})
	return account(r, err)
}

// Library implements catalog.Source.
func (c *Client) Library(ctx context.Context) (catalog.Library, error) {
	r, err := c.Call(ctx, api.Call{Method: api.MethodLibrary})
	switch {
	case err != nil:
		return catalog.Library{}, err
	case r.Library == nil:
		return catalog.Library{}, errNoResult
	}
	return *r.Library, nil
}

// Like implements catalog.Source.
func (c *Client) Like(ctx context.Context, songID string, liked bool) error {
	_, err := c.Call(ctx, api.Call{Method: api.MethodLike, Like: &api.LikeArgs{ID: songID, Liked: liked}})
	return err
}

// SignIn implements catalog.Accounts: the daemon cleans the credentials,
// checks and stores them.
func (c *Client) SignIn(ctx context.Context, credentials string) (catalog.Account, error) {
	r, err := c.Call(ctx, api.Call{Method: api.MethodSignIn, SignIn: &api.SignInArgs{Credentials: credentials}})
	return account(r, err)
}

// SignOut implements catalog.Accounts. It waits for the reply, or the
// connection's end, as the interface takes no ctx.
func (c *Client) SignOut() error {
	_, err := c.Call(context.Background(), api.Call{Method: api.MethodSignOut})
	return err
}

func account(r *api.Reply, err error) (catalog.Account, error) {
	switch {
	case err != nil:
		return catalog.Account{}, err
	case r.Account == nil:
		return catalog.Account{}, errNoResult
	}
	return *r.Account, nil
}

// Refresh has the daemon fetch part anew (the session's Retry); the
// result comes as an event.
func (c *Client) Refresh(ctx context.Context, part api.Part) error {
	_, err := c.Call(ctx, api.Call{Method: api.MethodRefresh, Part: &api.PartArgs{Part: part}})
	return err
}

// Status returns the daemon's snapshot for `ytm status`.
func (c *Client) Status(ctx context.Context) (api.Status, error) {
	r, err := c.Call(ctx, api.Call{Method: api.MethodStatus})
	switch {
	case err != nil:
		return api.Status{}, err
	case r.Status == nil:
		return api.Status{}, errNoResult
	}
	return *r.Status, nil
}

// Ping returns once the daemon answers.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.Call(ctx, api.Call{Method: api.MethodPing})
	return err
}

// Handoff asks the daemon to save and exit for a newer one, which the
// caller starts (ipc.SpawnDaemon) within wait; it returns once the daemon
// closed the connection. A daemon handing off already never replies; it
// returns as well when it exits.
func (c *Client) Handoff(ctx context.Context, wait time.Duration) error {
	return c.untilGone(ctx, nil, &api.Call{Method: api.MethodHandoff, Hand: &api.HandoffArgs{Wait: wait}})
}

// Shutdown stops the daemon (and playback); it returns once the daemon
// closed the connection.
func (c *Client) Shutdown(ctx context.Context) error {
	return c.untilGone(ctx, nil, &api.Call{Method: api.MethodShutdown})
}

// untilGone sends call, after which the daemon exits, over l (the current
// link if nil), and waits until the connection is gone.
func (c *Client) untilGone(ctx context.Context, l *link, call *api.Call) error {
	if l == nil {
		l = c.current()
	}
	if l == nil {
		return api.ErrDisconnected
	}
	if _, err := c.callOn(ctx, l, call); err != nil && !isDisconnected(err) {
		return err
	}
	select {
	case <-l.closed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
