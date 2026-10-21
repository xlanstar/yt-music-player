package main

import (
	"runtime"
	"strconv"
	"yt-music-player/internal/api"
	"yt-music-player/internal/fetchstate"
	"yt-music-player/internal/session"
	"yt-music-player/internal/theme"
	"yt-music-player/locales"

	"github.com/egoist/mygo/ui"
)

const musicURL = "https://music.youtube.com"

// accountButton shows nothing while the account loads or after it
// failed: the library page reports that.
func (a *app) accountButton(c *ui.Context) {
	if !a.session.IsSignedIn() {
		if a.fetches.State(session.AccountRes).State != fetchstate.SignedOut {
			return
		}
		signIn := pill(c, "sign-in", nil, locales.SignIn(), pillOpts{style: pillLight, size: pillMedium}).
			Margin(0, 0, 0, th.Space(1.5))
		if signIn.Clicked() {
			a.openSignIn()
		}
		return
	}
	user, name := a.session.Account(), a.session.DisplayName()
	button := ui.ButtonBase(c.Key("account")).Size(34, 34).Radius(17).Center().Margin(0, 0, 0, th.Space(1.5)).
		Label(user.Name).Cursor(ui.CursorPointer).KeepFocus()
	tip(c, button, name, "")
	button.Children(func() {
		if user.Photo.IsURL() {
			coverArt(c, user.Photo, 34, 17)
		} else {
			ui.Avatar(c, user.Name, nil).Size(34, 34)
		}
	})
	button.Menu(func(m *ui.Menu) {
		m.Item(locales.SignedInAs(name)).Disabled(true)
		m.Separator()
		if m.Item(locales.SignOut()).Disabled(a.session.IsSigningOut()).Chosen() {
			a.session.SignOut()
		}
	})
}

var devtoolsKeys = func() string {
	if runtime.GOOS == "darwin" {
		return "⌥⌘I"
	}
	return "Ctrl+Shift+I"
}()

// devtoolsStep is formatted once per language, not per build.
func (a *app) devtoolsStep() string {
	m := &a.memo.devtoolsStep
	if *m == "" {
		*m = locales.SignInDevtools(devtoolsKeys)
	}
	return *m
}

// signInSheet asks for the Cookie header of a signed-in YouTube Music
// tab: the only credentials usable without Google's sign-in flow.
func (a *app) signInSheet(c *ui.Context) {
	busy := a.session.IsSigningIn()
	ty, col := &th.Type, &th.Color
	ui.DialogBase(c, &a.sheet.isOpen, func(back, panel ui.Element) {
		sheetFrame(back, panel, 540, locales.SignInTitle()).Gap(th.Space(4.5))
		sheetHeader(c, locales.SignInTitle(), locales.SignInIntro())
		ui.Column(c).Gap(th.Space(2)).Children(func() {
			step(c, 1, "", func() {
				ui.Text(c, locales.SignInOpenBefore())
				ui.Link(c, "music.youtube.com", musicURL)
				ui.Text(c, locales.SignInOpenAfter())
			})
			step(c, 2, a.devtoolsStep(), nil)
			step(c, 3, locales.SignInPickRequest(), nil)
			step(c, 4, locales.SignInPaste(), nil)
		})
		field := ui.TextArea(c.Key(a.sheet.attempt), &a.sheet.credentials).Lines(3, 6).FontSize(ty.Sub).
			Label(locales.CookieHeader()).Placeholder(locales.CookiePlaceholder()).AutoFocus().ReadOnly(busy)
		submit := field.Shortcut(ui.Cmd, ui.KeyEnter)
		if a.sheet.failure != "" {
			ui.Text(c, locales.SignInFailedReason(a.sheet.failure)).FontSize(ty.Detail).TextColor(col.Error).MaxLines(4)
		}
		ui.Text(c, locales.SignInPrivacy()).FontSize(ty.Small).TextColor(col.Faint)
		ui.Row(c).Gap(th.Space(2.5)).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			if busy {
				ui.Text(c, locales.SigningIn()).FontSize(ty.Detail).TextColor(col.Muted).Role(ui.RoleStatus).Grow(1)
			}
			if pill(c, "cancel", nil, locales.Cancel(), pillOpts{style: pillGhost}).Clicked() {
				a.sheet.isOpen = false
			}
			label := locales.SignIn()
			if busy {
				label = locales.SigningIn()
			}
			if sheetButton(c, label, busy || a.noCredentials()).Clicked() {
				submit = true
			}
		})
		if submit && !busy {
			a.signIn()
		}
	})
}

// noCredentials cleans once per edit: cleaning a pasted header copies it.
func (a *app) noCredentials() bool {
	m := &a.memo.noCredentials
	if m.stale(a.sheet.credentials) {
		m.v = api.CleanCredentials(a.sheet.credentials) == ""
	}
	return m.v
}

// step shows text, or the rich-text runs builds. Plain text is one
// ui.Text, as a paragraph copies its runs' text in every build.
func step(c *ui.Context, number int, text string, runs func()) {
	ui.Row(c).Gap(th.Space(2.5)).AlignItems(ui.Start).Children(func() {
		ui.Box(c).Size(22, 22).Shrink(0).Radius(11).Center().Background(th.Color.Raised).Children(func() {
			ui.Text(c, strconv.Itoa(number)).FontSize(th.Type.Small).FontWeight(700)
		})
		if runs == nil {
			ui.Text(c, text).FontSize(th.Type.Label).LineHeight(1.45).TextColor(th.Color.Text).Grow(1).MinWidth(0)
			return
		}
		ui.RichText(c).FontSize(th.Type.Label).LineHeight(1.45).TextColor(th.Color.Text).Grow(1).MinWidth(0).
			Children(runs)
	})
}

// sheetButton looks like pillLight, grayed out while disabled.
func sheetButton(c *ui.Context, label string, disabled bool) ui.Element {
	button := ui.ButtonBase(c.Key("sign-in-submit")).Height(40).Padding(0, th.Space(5.5)).
		Radius(th.Radius.Pill).Center().Shrink(0).Cursor(ui.CursorPointer).Disabled(disabled)
	bg := th.Color.Text
	switch {
	case button.IsDisabled():
		bg = th.Color.Text.Alpha(0.35)
	case button.Hovered():
		bg = th.Color.Light
	}
	button.Background(bg).TextColor(th.Color.Base).Transition(colorFade).Children(func() {
		ui.Text(c, label).FontSize(th.Type.Label).FontWeight(600).SingleLine()
	})
	return button
}

func (a *app) signedOutPage(c *ui.Context, icon *ui.SVG, title, body string) {
	signIn := func() {
		if pill(c, "sign-in-page", nil, locales.SignIn(), pillOpts{style: pillLight}).Margin(th.Space(3), 0, 0, 0).Clicked() {
			a.openSignIn()
		}
	}
	scrollPage(c, func() { message(c, icon, title, body, 1, signIn) })
}

// likedPage shows likes and unlikes since the fetch at once.
func (a *app) likedPage(c *ui.Context, width float32) {
	switch fetch := a.feedState(session.LikedRes); fetch.State {
	case fetchstate.SignedOut:
		a.signedOutPage(
			c,
			icHeart,
			locales.LikesSignInTitle(),
			locales.LikesSignInDetail(),
		)
	case fetchstate.Failed:
		loadFailed(c, locales.LikesFailed(), fetch.Err, func() { a.session.Retry(session.LikedRes) })
	case fetchstate.Loading:
		collectionSkeleton(c, width, locales.AutoPlaylist())
	default:
		a.collectionPage(c, width, a.likedCollection())
	}
}

// libraryState reports whether it showed a signed-out, failed or loading
// state.
func (a *app) libraryState(c *ui.Context, width float32, header func()) bool {
	switch fetch := a.feedState(session.LibraryRes); fetch.State {
	case fetchstate.SignedOut:
		a.signedOutPage(
			c,
			icLibrary,
			locales.LibrarySignInTitle(),
			locales.LibrarySignInDetail(),
		)
	case fetchstate.Failed:
		loadFailed(c, locales.LibraryFailed(), fetch.Err, func() { a.session.Retry(session.LibraryRes) })
	case fetchstate.Loading:
		librarySkeleton(c, width, header)
	default:
		return false
	}
	return true
}

func librarySkeleton(c *ui.Context, width float32, header func()) {
	cols, gap := libraryColumns(width), libraryGap()
	cell := (width - gap*float32(cols-1)) / float32(cols)
	ui.Column(c).Grow(1).Padding(th.Space(1), th.Layout.Page-rowBleed, th.Space(12)).Children(func() {
		header()
		ui.Column(c).Padding(0, rowBleed).Gap(th.Space(3.5)).Children(func() {
			loadingStatus(c)
			ui.Grid(c).Columns(cols).GapX(gap).Children(func() {
				for range cols {
					boneCard(c, 0, cell, 0.45)
				}
			})
		})
	})
}

var toastMotion = ui.ElementTransition{Enter: &ui.Motion{Y: 8}, Exit: &ui.Motion{}}

// toasts draws the window's toasts in the theme's look, above a bottom
// player bar, whose controls MyGo's default placement would cover.
func toasts(c *ui.Context) {
	t := c.Theme()
	ty, col := &th.Type, &th.Color
	bottom := th.Layout.Player + th.Layout.Gutter
	if th.Layout.PlayerBar == theme.Top {
		bottom = th.Space(6)
	}
	ui.ToastViewportBase(c, func(viewport ui.Element, list []ui.Toast) {
		viewport.Padding(0, 0, bottom, 0).AlignItems(ui.Center).Gap(th.Space(2))
		for _, item := range list {
			toast := ui.ToastBase(c, item)
			toast.Root.Row().AlignItems(ui.Center).Gap(th.Space(4)).
				Padding(th.Space(2.5), th.Space(3), th.Space(2.5), th.Space(4)).
				Radius(th.Radius.Control).MaxWidth(560).
				Background(t.Inverse).TextColor(t.InverseText).Shadow(0, 8, 24, 0, col.Shadow.Alpha(0.4)).
				Transition(toastMotion)
			toast.Root.Children(func() {
				ui.Column(c).Shrink(1).Gap(th.Space(0.5)).Children(func() {
					ui.Text(c, item.Title).FontSize(ty.Label).FontWeight(600)
					if item.Description != "" {
						ui.Text(c, item.Description).FontSize(ty.Sub).TextColor(col.Muted).MaxLines(3)
					}
				})
				if item.Action != "" {
					action := toast.ActionButton().Padding(th.Space(1.5), th.Space(2.5)).
						Radius(th.Radius.Small).Cursor(ui.CursorPointer)
					if action.Hovered() {
						action.Background(col.GhostHover)
					}
					action.Children(func() {
						ui.Text(c, item.Action).FontSize(ty.Detail).FontWeight(600).
							TextColor(col.Accent.Mix(col.Text, 0.35))
					})
				}
				closeButton := toast.CloseButton().Size(28, 28).Radius(14).Center().
					Label(locales.Dismiss()).Cursor(ui.CursorPointer)
				closeButton.Children(func() { ui.Icon(c, icClose).Size(14, 14).TextColor(th.Color.Muted) })
			})
		}
	})
}
