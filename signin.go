package main

import (
	"runtime"
	"strconv"
	"yt-music-player/internal/fetchstate"
	"yt-music-player/internal/session"
	"yt-music-player/internal/theme"

	"github.com/egoist/mygo/ui"
)

const musicURL = "https://music.youtube.com"

// accountButton shows nothing while the account loads or after it
// failed: the library page reports that.
func (a *app) accountButton(c *ui.Context) {
	canSignIn := a.session.CanSignIn()
	if !a.session.IsSignedIn() {
		if !canSignIn || a.fetches.State(session.AccountRes).State != fetchstate.SignedOut {
			return
		}
		signIn := pill(c, "sign-in", nil, "Sign in", pillLight).
			Height(34).Padding(0, th.Space(4)).Margin(0, 0, 0, th.Space(1.5))
		if signIn.Clicked() {
			a.openSignIn()
		}
		return
	}
	account, name := a.session.Account(), a.session.DisplayName()
	button := ui.ButtonBase(c.Key("account")).Size(34, 34).Radius(17).Center().Margin(0, 0, 0, th.Space(1.5)).
		Label(account.Name).Cursor(ui.CursorPointer).KeepFocus()
	tip(c, button, name, "")
	button.Children(func() {
		if isURL(account.Photo) {
			coverArt(c, account.Photo, 34, 34, 17)
		} else {
			ui.Avatar(c, account.Name, nil).Size(34, 34)
		}
	})
	button.Menu(func(m *ui.Menu) {
		m.Item("Signed in as " + name).Disabled(true)
		if !canSignIn {
			return
		}
		m.Separator()
		if m.Item("Sign out").Disabled(a.session.IsSigningOut()).Chosen() {
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

// devtoolsStep is joined once, not per build.
var devtoolsStep = "Open its developer tools (" + devtoolsKeys + "), then the Network tab, and reload."

// signInSheet asks for the Cookie header of a signed-in YouTube Music
// tab: the only credentials usable without Google's sign-in flow.
func (a *app) signInSheet(c *ui.Context) {
	busy := a.session.IsSigningIn()
	ty, col := &th.Type, &th.Color
	ui.DialogBase(c, &a.sheet.isOpen, func(back, panel ui.Element) {
		back.Background(col.Shadow.Alpha(0.6))
		panel.Width(540).Padding(th.Space(7)).Gap(th.Space(4.5)).
			Radius(th.Radius.Dialog).Background(col.Panel).Border(1, col.Line).
			Shadow(0, 24, 60, -8, col.Shadow.Alpha(0.6)).Label("Sign in to YouTube Music")
		ui.Column(c).Gap(th.Space(1.5)).Children(func() {
			heading(c, "Sign in to YouTube Music", ty.Section, 1)
			ui.Text(c, "Your library and likes come from your account, through your browser’s session.").
				FontSize(ty.Label).TextColor(col.Muted)
		})
		ui.Column(c).Gap(th.Space(2)).Children(func() {
			step(c, 1, "", func() {
				ui.Text(c, "Open ")
				ui.Link(c, "music.youtube.com", musicURL)
				ui.Text(c, " in your browser, signed in.")
			})
			step(c, 2, devtoolsStep, nil)
			step(c, 3, "Pick any request to music.youtube.com and copy the value of its Cookie request header.", nil)
			step(c, 4, "Paste it here.", nil)
		})
		field := ui.TextArea(c.Key(a.sheet.attempt), &a.sheet.credentials).Lines(3, 6).FontSize(ty.Sub).
			Label("Cookie header").Placeholder("Paste the Cookie header").AutoFocus().ReadOnly(busy)
		submit := field.Shortcut(ui.Cmd, ui.KeyEnter)
		if a.sheet.failure != "" {
			ui.Text(c, "Couldn’t sign in: "+a.sheet.failure).FontSize(ty.Detail).TextColor(col.Error).MaxLines(4)
		}
		ui.Text(c, "It stays on this computer, and goes only to YouTube Music.").FontSize(ty.Small).TextColor(col.Faint)
		ui.Row(c).Gap(th.Space(2.5)).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			if busy {
				ui.Text(c, "Signing in…").FontSize(ty.Detail).TextColor(col.Muted).Role(ui.RoleStatus).Grow(1)
			}
			if pill(c, "cancel", nil, "Cancel", pillGhost).Clicked() {
				a.sheet.isOpen = false
			}
			label := "Sign in"
			if busy {
				label = "Signing in…"
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
		m.v = session.CleanCredentials(a.sheet.credentials) == ""
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
	var signIn func()
	if a.session.CanSignIn() {
		signIn = func() {
			if pill(c, "sign-in-page", nil, "Sign in", pillLight).Margin(th.Space(3), 0, 0, 0).Clicked() {
				a.openSignIn()
			}
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
			"Sign in to see your likes",
			"Songs you like on YouTube Music show up here.",
		)
	case fetchstate.Failed:
		loadFailed(c, "Couldn’t load your likes", fetch.Err, func() { a.session.Retry(session.LikedRes) })
	case fetchstate.Loading:
		collectionSkeleton(c, width, "Auto playlist")
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
			"Sign in to see your library",
			"Your playlists, songs, albums and artists from YouTube Music show up here.",
		)
	case fetchstate.Failed:
		loadFailed(c, "Couldn’t load your library", fetch.Err, func() { a.session.Retry(session.LibraryRes) })
	case fetchstate.Loading:
		librarySkeleton(c, width, header)
	default:
		return false
	}
	return true
}

func librarySkeleton(c *ui.Context, width float32, header func()) {
	cols := columnsFor(width, th.Layout.Sized().Cell, 2, 7)
	cell := (width - th.Space(5)*float32(cols-1)) / float32(cols)
	ui.Column(c).Grow(1).Padding(th.Space(1), th.Layout.Page-rowBleed, th.Space(12)).Children(func() {
		header()
		ui.Column(c).Padding(0, rowBleed).Gap(th.Space(3.5)).Children(func() {
			loadingStatus(c)
			ui.Grid(c).Columns(cols).GapX(th.Space(5)).Children(func() {
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
					Label("Dismiss").Cursor(ui.CursorPointer)
				closeButton.Children(func() { ui.Icon(c, icClose).Size(14, 14).TextColor(th.Color.Muted) })
			})
		}
	})
}
