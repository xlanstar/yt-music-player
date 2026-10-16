package main

import (
	"yt-music-player/internal/account"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/notice"
	"yt-music-player/internal/noticetext"
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

// connectAccount makes the account, which talks to src, and the session,
// which keeps what the account reports for the pages: in one process, a
// report is a call.
func (a *app) connectAccount(src catalog.Source, cat *catalog.Catalog) {
	a.account = account.New(src, a.post, func(c account.Change) { a.session.Apply(c) })
	a.session = session.New(a.account, &a.fetches, a.loader, cat)
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
	t := ui.Toast{ID: n.Topic, Title: title, Description: detail}
	switch n.Kind {
	case notice.Failure:
		t.Type = "error"
	case notice.NeedsAccount:
		if a.session.CanSignIn() {
			t.Action, t.OnAction = locales.SignIn(), a.openSignIn
		}
	case notice.SignedIn:
		a.sheet.isOpen = false
	case notice.SignInError:
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
	if a.session.CanSignIn() {
		a.sheet.isOpen, a.sheet.failure = true, ""
	}
}

// signIn sends the pasted credentials and forgets them, undo history
// included.
func (a *app) signIn() {
	credentials := a.sheet.credentials
	a.sheet.credentials = ""
	a.sheet.attempt++
	if a.session.CanSignIn() && api.CleanCredentials(credentials) != "" {
		a.sheet.failure = ""
		a.session.SignIn(credentials)
	}
}
