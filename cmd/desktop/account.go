package main

import (
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/notice"
	"yt-music-player/internal/noticetext"
	"yt-music-player/internal/remote"
	"yt-music-player/internal/session"
	"yt-music-player/locales"

	"github.com/egoist/mygo/ui"
)

// sheetState's attempt keys the field so a new one drops the old undo
// history, which holds the credentials too.
type sheetState struct {
	isOpen      bool
	credentials string
	attempt     int
	failure     string
}

// connectAccount makes the session before the client connects, giving it
// the account's events; the session fetches the library and likes through
// ytmd once it tells who is signed in. post is the Client's: the fetches
// answer through ytmd, as its events do.
func (a *app) connectAccount(cat *catalog.Catalog, post func(func())) {
	a.session = session.New(session.Options{
		Daemon: remote.NewAccount(a.client), Source: a.client,
		Post: post, Fetches: &a.fetches, Loader: a.loader, Catalog: cat,
	})
	a.client.SetAccountEvents(a.session)
}

func (a *app) syncAccount(c *ui.Context) {
	for _, n := range a.session.TakeNotices() {
		a.tell(c, n)
	}
	// Escape or a click outside closes the sheet without a callback, so
	// clear its input and cancel its sign-in here.
	if !a.sheet.isOpen {
		a.sheet.credentials, a.sheet.failure = "", ""
		a.session.CancelSignIn()
	}
}

// tell is the one path for every notice: a toast, or the sign-in
// sheet's error.
func (a *app) tell(c *ui.Context, n notice.Notice) {
	title, detail := noticetext.Render(n)
	t := ui.Toast{ID: string(n.Topic), Title: title, Description: detail}
	switch n.Kind {
	case notice.KindFailure:
		t.Type = "error"
	case notice.KindNeedsAccount:
		t.Action, t.OnAction = locales.SignIn(), a.openSignIn
	case notice.KindSignedIn:
		a.sheet.isOpen = false
	case notice.KindSignInError:
		a.sheet.failure = detail
		c.Announce(locales.Announcement(title, detail))
		return
	}
	c.AddToast(t)
}

func (a *app) likedCount() int {
	m := &a.memo.liked
	if m.stale(a.session.LikesGen()) {
		m.v = a.session.LikedCount()
	}
	return m.v
}

func (a *app) openSignIn() {
	a.sheet.isOpen, a.sheet.failure = true, ""
}

// signIn sends the pasted credentials and forgets them, undo history
// included.
func (a *app) signIn() {
	credentials := a.sheet.credentials
	a.sheet.credentials = ""
	a.sheet.attempt++
	if api.CleanCredentials(credentials) != "" {
		a.sheet.failure = ""
		a.session.SignIn(credentials)
	}
}
