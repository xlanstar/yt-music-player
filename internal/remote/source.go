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
	return resultOf(r, err, func(r *api.Reply) *catalog.Results { return r.Results })
}

// Album is catalog.Source's Album, through the daemon.
func (c *Client) Album(ctx context.Context, id string) (catalog.Album, []catalog.Song, error) {
	r, err := c.Call(ctx, api.Call{Method: api.MethodAlbum, Ref: &api.RefArgs{ID: id}})
	album, err := resultOf(r, err, func(r *api.Reply) *api.AlbumReply { return r.Album })
	return album.Album, album.Songs, err
}

// Playlist is catalog.Source's Playlist, through the daemon.
func (c *Client) Playlist(ctx context.Context, id string) (catalog.Playlist, []catalog.Song, error) {
	r, err := c.Call(ctx, api.Call{Method: api.MethodPlaylist, Ref: &api.RefArgs{ID: id}})
	playlist, err := resultOf(r, err, func(r *api.Reply) *api.PlaylistReply { return r.Playlist })
	return playlist.Playlist, playlist.Songs, err
}

// Home is catalog.Source's Home, through the daemon.
func (c *Client) Home(ctx context.Context) (catalog.Home, error) {
	r, err := c.Call(ctx, api.Call{Method: api.MethodHome})
	return resultOf(r, err, func(r *api.Reply) *catalog.Home { return r.Home })
}

// Explore is catalog.Source's Explore, through the daemon.
func (c *Client) Explore(ctx context.Context) (catalog.Explore, error) {
	r, err := c.Call(ctx, api.Call{Method: api.MethodExplore})
	return resultOf(r, err, func(r *api.Reply) *catalog.Explore { return r.Explore })
}

// Library is catalog.Source's Library, through the daemon: the session's,
// fetched once ytmd tells who is signed in.
func (c *Client) Library(ctx context.Context) (catalog.Library, error) {
	r, err := c.Call(ctx, api.Call{Method: api.MethodLibrary})
	return resultOf(r, err, func(r *api.Reply) *catalog.Library { return r.Library })
}

// SignIn has the daemon clean, check and store credentials.
func (c *Client) SignIn(ctx context.Context, credentials string) (catalog.Account, error) {
	r, err := c.Call(ctx, api.Call{Method: api.MethodSignIn, SignIn: &api.SignInArgs{Credentials: credentials}})
	return resultOf(r, err, accountOf)
}

// Status returns the daemon's snapshot for `ytm status`.
func (c *Client) Status(ctx context.Context) (api.Status, error) {
	r, err := c.Call(ctx, api.Call{Method: api.MethodStatus})
	return resultOf(r, err, func(r *api.Reply) *api.Status { return r.Status })
}

// resultOf is what pick finds in a reply to a call that ended with err;
// a reply without it is errNoResult.
func resultOf[T any](r *api.Reply, err error, pick func(*api.Reply) *T) (T, error) {
	var zero T
	if err != nil {
		return zero, err
	}
	v := pick(r)
	if v == nil {
		return zero, errNoResult
	}
	return *v, nil
}

func accountOf(r *api.Reply) *catalog.Account { return r.Account }

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
