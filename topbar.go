package main

import (
	"yt-music-player/internal/theme"

	"github.com/egoist/mygo/ui"
)

func (a *app) topBar(c *ui.Context, width, panelShown float32) {
	// The window controls sit over the top bar's corners unless the
	// sidebar or the panel (as it slides in) covers them.
	layout := &th.Layout
	var left, right float32
	if layout.SidebarSide != theme.Left {
		left = titleBarRoom(c, theme.Left, panelShown*boolToFloat(layout.PanelSide == theme.Left))
	}
	if layout.SidebarSide != theme.Right {
		right = titleBarRoom(c, theme.Right, panelShown*boolToFloat(layout.PanelSide == theme.Right))
	}
	bar := ui.Row(c).Height(layout.Header).Shrink(0).Padding(0, th.Space(5)+right, 0, th.Space(5)+left).
		Gap(th.Space(2)).AlignItems(ui.Center).DragWindow()
	bar.Children(func() {
		ui.Row(c).Gap(th.Space(2)).AlignItems(ui.Center).FocusGroup(ui.Horizontal).Children(func() {
			history := iconOpts{bg: th.Color.Shadow.Alpha(0.25), isDisabled: !a.router.CanGoBack(), keys: hints.back}
			if iconButton(c, "back", icChevronLeft, "Back", 34, 18, history).Clicked() {
				a.router.Back()
			}
			history.isDisabled, history.keys = !a.router.CanGoForward(), hints.forward
			if iconButton(c, "forward", icChevronRight, "Forward", 34, 18, history).Clicked() {
				a.router.Forward()
			}
		})
		ui.Box(c).Width(th.Space(2))
		a.searchBox(c, width > 560)
		ui.Spacer(c)
		iconButton(c, "notifications", icBell, "Notifications", 36, 18, iconOpts{})
		if iconButton(c, "appearance", icPalette, "Appearance", 36, 18, iconOpts{keys: hints.appearance}).Clicked() {
			a.openLook()
		}
		a.accountButton(c)
	})
}

func (a *app) searchBox(c *ui.Context, roomy bool) {
	box := ui.Row(c).Height(40).Width(440).Shrink(1).MinWidth(150).Padding(0, th.Space(1.5), 0, th.Space(4)).
		Gap(th.Space(2.5)).Radius(th.Radius.Pill).AlignItems(ui.Center).Cursor(ui.CursorText)
	focused := a.search.field.Focused(c)
	switch {
	case focused:
		// The input draws no focus ring, so the box does; the padding
		// shrinks by the wider border.
		box.Background(th.Color.Hover).Border(2, th.Color.Focus).Padding(0, th.Space(1.5)-1, 0, th.Space(4)-1)
	case box.Hovered():
		box.Background(th.Color.Hover).Border(1, th.Color.Line)
	default:
		box.Background(th.Color.Field).Border(1, th.Color.Line)
	}
	placeholder := "Search"
	if roomy {
		placeholder = "Search songs, albums, artists"
	}
	box.Children(func() {
		ui.Icon(c, icSearch).Size(16, 16).TextColor(th.Color.Muted)
		input := ui.TextInputBase(c.Key("search"), &a.search.query).Grow(1).FontSize(th.Type.Title).
			Placeholder(placeholder).Label("Search").Description(hints.search).FocusRing(false).Bind(&a.search.field).
			OnChange(a.actions.search)
		if input.Shortcut(0, ui.KeyEscape) {
			a.search.query = ""
			a.leaveSearch()
		}
		if a.search.query != "" {
			if iconButton(c, "clear", icClose, "Clear search", 28, 14, iconOpts{}).Clicked() {
				a.search.query = ""
				a.leaveSearch()
				a.search.field.Focus(c)
			}
			return
		}
		if !roomy {
			return
		}
		ui.Text(c, hints.search).FontSize(th.Type.Caption).FontWeight(600).TextColor(th.Color.Faint).
			Padding(th.Space(0.75), th.Space(1.75)).Margin(0, th.Space(1.5), 0, 0).
			Radius(th.Radius.Thumb).Border(1, th.Color.Line)
	})
}
