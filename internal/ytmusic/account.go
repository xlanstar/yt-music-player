package ytmusic

import (
	"context"
	"errors"
	"fmt"
	"yt-music-player/internal/auth"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/innertube"
)

var errRejected = errors.New("ytmusic: YouTube Music did not accept these credentials; " +
	"copy them again from a signed-in music.youtube.com tab")

// loadStored signs in with the stored credentials; c.load runs it once.
// A failed load (none saved, a denied Keychain prompt, a corrupt entry)
// leaves the client signed out; the next SignIn replaces the entry.
func (c *Client) loadStored() {
	if c.store == nil {
		return
	}
	creds, err := c.store.Load()
	if err != nil {
		return
	}
	c.api.Auth.Set(creds.Decorator(nil))
}

func (c *Client) signedCall(ctx context.Context, endpoint string, body map[string]any) (*response, error) {
	c.load()
	return signedCallWith(ctx, c.api, endpoint, body)
}

// signedCallWith fails with catalog.ErrSignedOut without credentials
// (sending no request), when InnerTube refuses them, or when it answers
// as to a visitor, as it does for an expired session.
func signedCallWith(
	ctx context.Context,
	api *innertube.Client,
	endpoint string,
	body map[string]any,
) (*response, error) {
	if !api.Auth.IsSignedIn() {
		return nil, fmt.Errorf("ytmusic: %s: %w", endpoint, catalog.ErrSignedOut)
	}
	resp, err := callWith(ctx, api, endpoint, body)
	if errors.Is(err, innertube.ErrUnauthorized) {
		return nil, fmt.Errorf("ytmusic: %s: %w: %w", endpoint, catalog.ErrSignedOut, err)
	}
	if err != nil {
		return nil, err
	}
	if signedOut(resp) {
		return nil, fmt.Errorf("ytmusic: %s: %w (answered as to a visitor)", endpoint, catalog.ErrSignedOut)
	}
	return resp, nil
}

// signedOut reports a response for a visitor: InnerTube's tracking says
// logged_in 0 or, without tracking, the page asks to sign in.
func signedOut(resp *response) bool {
	for _, svc := range resp.ResponseContext.ServiceTrackingParams {
		for _, p := range svc.Params {
			if p.Key == "logged_in" {
				return p.Value == "0"
			}
		}
	}
	for _, sec := range sectionList(resp).sections() {
		if sec.ItemSection == nil {
			continue
		}
		for _, v := range sec.ItemSection.Contents {
			if v.Message.Button.Renderer.Endpoint.SignIn != nil {
				return true
			}
		}
	}
	return false
}

// SignIn implements catalog.Accounts for what auth.Parse accepts. The
// credentials are tried on the account menu, then saved, then used; a
// failure at any step leaves the client as it was.
func (c *Client) SignIn(ctx context.Context, credentials string) (catalog.Account, error) {
	c.load()
	creds, err := auth.Parse(credentials)
	if err != nil {
		return catalog.Account{}, fmt.Errorf("ytmusic: sign in: %w", err)
	}
	decorator := creds.Decorator(nil)
	trial := *c.api
	trial.Auth = &innertube.Auth{}
	trial.Auth.Set(decorator)
	acct, err := account(ctx, &trial)
	switch {
	case errors.Is(err, catalog.ErrSignedOut):
		// ErrSignedOut would read as the current account signing out.
		return catalog.Account{}, errRejected
	case err != nil:
		return catalog.Account{}, fmt.Errorf("ytmusic: sign in: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.store != nil {
		if err := c.store.Save(creds); err != nil {
			return catalog.Account{}, fmt.Errorf("ytmusic: sign in: %w", err)
		}
	}
	c.api.Auth.Set(decorator)
	return acct, nil
}

// SignOut implements catalog.Accounts. The client signs out even when
// the store fails to clear, which the error then reports.
func (c *Client) SignOut() error {
	c.load()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.api.Auth.Set(nil)
	if c.store != nil {
		if err := c.store.Clear(); err != nil {
			return fmt.Errorf("ytmusic: sign out: %w", err)
		}
	}
	return nil
}

// Account returns who is signed in, from the account menu.
func (c *Client) Account(ctx context.Context) (catalog.Account, error) {
	c.load()
	return account(ctx, c.api)
}

func account(ctx context.Context, api *innertube.Client) (catalog.Account, error) {
	resp, err := signedCallWith(ctx, api, "account/account_menu", map[string]any{})
	if err != nil {
		return catalog.Account{}, fmt.Errorf("ytmusic: account: %w", err)
	}
	acct, ok := accountMenu(resp)
	if !ok {
		// A signed-in menu always has the account header.
		return catalog.Account{}, fmt.Errorf("ytmusic: account: %w (no account header)", catalog.ErrSignedOut)
	}
	return acct, nil
}

func accountMenu(resp *response) (catalog.Account, bool) {
	if len(resp.Actions) == 0 || resp.Actions[0].OpenPopup.Popup.Menu.Header.Account == nil {
		return catalog.Account{}, false
	}
	h := resp.Actions[0].OpenPopup.Popup.Menu.Header.Account
	acct := catalog.Account{
		Name:   h.AccountName.String(),
		Handle: h.ChannelHandle.String(),
		Photo:  catalog.Art(h.AccountPhoto.largest()),
	}
	return acct, acct.Name != ""
}

// Like likes or unlikes a song, as the thumbs-up button does.
func (c *Client) Like(ctx context.Context, songID string, liked bool) error {
	c.load()
	if !c.api.Auth.IsSignedIn() {
		return fmt.Errorf("ytmusic: like %q: %w", songID, catalog.ErrSignedOut)
	}
	if songID == "" {
		return errors.New("ytmusic: like: empty song id")
	}
	endpoint := "like/removelike"
	if liked {
		endpoint = "like/like"
	}
	body := map[string]any{"target": map[string]any{"videoId": songID}}
	if _, err := c.signedCall(ctx, endpoint, body); err != nil {
		return fmt.Errorf("ytmusic: like %q: %w", songID, err)
	}
	return nil
}
