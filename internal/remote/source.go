package remote

import (
	"context"
	"errors"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
)

// Search is catalog.Source's Search, through the daemon.
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

// Album is catalog.Source's Album, through the daemon.
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

// Playlist is catalog.Source's Playlist, through the daemon.
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

// Home is catalog.Source's Home, through the daemon.
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

// Explore is catalog.Source's Explore, through the daemon.
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

// SignIn has the daemon clean, check and store credentials.
func (c *Client) SignIn(ctx context.Context, credentials string) (catalog.Account, error) {
	r, err := c.Call(ctx, api.Call{Method: api.MethodSignIn, SignIn: &api.SignInArgs{Credentials: credentials}})
	return account(r, err)
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

// Handoff asks the daemon to save and exit for a newer one, which the
// caller starts within wait; it returns once the daemon closed the
// connection.
func (c *Client) Handoff(ctx context.Context, wait time.Duration) error {
	return c.untilGone(ctx, nil, &api.Call{Method: api.MethodHandoff, Hand: &api.HandoffArgs{Wait: wait}})
}

// Shutdown stops the daemon (and playback); it returns once the daemon
// closed the connection.
func (c *Client) Shutdown(ctx context.Context) error {
	return c.untilGone(ctx, nil, &api.Call{Method: api.MethodShutdown})
}

// untilGone sends call, after which the daemon exits, over l (the current
// link if nil), and waits until the connection is gone: a daemon already
// handing off never replies.
func (c *Client) untilGone(ctx context.Context, l *link, call *api.Call) error {
	if l == nil {
		l = c.current()
	}
	if l == nil {
		return api.ErrDisconnected
	}
	if _, err := c.callOn(ctx, l, call); err != nil && !errors.Is(err, api.ErrDisconnected) {
		return err
	}
	select {
	case <-l.closed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
