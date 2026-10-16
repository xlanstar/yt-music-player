package main

import (
	"yt-music-player/internal/theme"
	"yt-music-player/locales"

	"github.com/egoist/mygo/ui"
)

// currentLyric is the line shown as being sung until playback can time
// lyrics.
const currentLyric = 2

// nowPlaying's offset is a margin before it.
func (a *app) nowPlaying(c *ui.Context, offset float32) {
	cv := coverOf(a.currentSong().Art)
	panel := ui.Column(c).Width(th.Layout.Panel).Shrink(0).Radius(th.Layout.PanelRadius).Clip().
		Background(th.Color.Panel)
	if offset != 0 {
		panel.Margin(0, 0, 0, offset)
	}
	b := &a.bar
	b.tint = panel.AnimateColor("tint", cv.to, ui.Smooth)
	if b.paintTint == nil {
		// Made once, like a.paints.
		b.paintTint = func(p *ui.Painter, r ui.Rect) {
			fade := ui.LinearGradient{From: b.tint.Alpha(0.35), To: b.tint.Alpha(0), Angle: 180, End: 0.65, Oklab: true}
			p.FillGradient(r, fade, th.Layout.PanelRadius)
		}
	}
	panel.Draw(b.paintTint)
	panel.Children(func() {
		left, right := th.Space(4), th.Space(3.5)
		if th.Layout.PanelSide == theme.Left {
			left += titleBarRoom(c, theme.Left, 0)
		} else {
			right += titleBarRoom(c, theme.Right, 0)
		}
		header := ui.Row(c).Height(th.Layout.Header).Shrink(0).Padding(0, right, 0, left).Gap(th.Space(2)).
			AlignItems(ui.Center).DragWindow()
		header.Children(func() {
			a.panelTabs(c)
			ui.Spacer(c)
			if iconButton(c, "close-panel", icPanel, locales.HidePanel(), 32, 17, iconOpts{}).Clicked() {
				a.panel.isShown = false
			}
		})
		ui.Scroll(c.Key(a.panel.tab)).Grow(1).Children(func() {
			if a.panel.tab == tabLyrics {
				a.lyricsView(c)
			} else {
				a.queueView(c)
			}
		})
	})
}

func (a *app) panelTabs(c *ui.Context) {
	tabs := ui.SegmentedBase(c.Key("panel-tabs"), &a.panel.tab, 2)
	tabs.Track.Padding(th.Space(0.75)).Radius(th.Radius.Pill).Background(th.Color.Shadow.Alpha(0.3)).Label(locales.Panel())
	tabs.Track.Children(func() {
		for i, name := range [...]string{locales.UpNext(), locales.Lyrics()} {
			seg := tabs.Segment(i).Height(30).Padding(0, th.Space(3.5)).Radius(th.Radius.Pill).Center().
				Cursor(ui.CursorPointer).KeepFocus()
			switch {
			case i == a.panel.tab:
				seg.Background(th.Color.Text).TextColor(th.Color.Base)
			case seg.Hovered():
				seg.TextColor(th.Color.Text)
			default:
				seg.TextColor(th.Color.Muted)
			}
			seg.Transition(colorFade)
			seg.Children(func() { ui.Text(c, name).FontSize(th.Type.Sub).FontWeight(600).SingleLine() })
		}
	})
}

func (a *app) queueView(c *ui.Context) {
	s := a.currentSong()
	cv := coverOf(s.Art)
	ui.Column(c).Padding(th.Space(1), th.Space(5), th.Space(6)).Gap(th.Space(4.5)).Children(func() {
		artSize := th.Layout.Panel - 2*th.Space(5)
		coverArt(c, s.Art, artSize, artSize, th.Radius.Large).Shadow(0, 24, 50, -10, cv.from.Alpha(0.5))
		ui.Row(c).Gap(th.Space(2)).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(th.Space(1.25)).Children(func() {
				heading(c, s.Title, th.Type.Section, 2).MaxLines(2)
				ui.Text(c, s.Artist).FontSize(th.Type.Lead).TextColor(th.Color.Muted).SingleLine()
			})
			a.likeButton(c, s.ID, 36)
			iconButton(c, "more", icMore, locales.More(), 36, 18, iconOpts{})
		})
		ui.Column(c).Gap(th.Space(1)).Margin(th.Space(1.5), 0, 0, 0).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Margin(0, 0, th.Space(2), 0).Children(func() {
				eyebrow(c, locales.UpNext()).Grow(1)
				ui.Row(c).Gap(th.Space(1.5)).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, icRadio).Size(13, 13).TextColor(th.Color.Faint)
					ui.Text(c, a.bar.fromAlbum(s.Album)).FontSize(th.Type.Small).TextColor(th.Color.Faint).SingleLine()
				})
			})
			ui.Column(c).Margin(0, -th.Space(2)).FocusGroup(ui.Vertical).Children(func() {
				for _, next := range a.upNext() {
					a.songRow(c, next, songRowOpts{art: 42, isCompact: true})
				}
			})
		})
	})
}

func (a *app) lyricsView(c *ui.Context) {
	s := a.currentSong()
	lyrics := a.cat.Lyrics(s.ID)
	ty := &th.Type
	ui.Column(c).Padding(th.Space(1), th.Space(6), th.Space(8)).Gap(th.Space(5.5)).Children(func() {
		ui.Row(c).Gap(th.Space(3)).AlignItems(ui.Center).Children(func() {
			coverArt(c, s.Art, 48, 48, th.Radius.Small)
			ui.Column(c).Grow(1).MinWidth(0).Gap(th.Space(0.75)).Children(func() {
				ui.Text(c, s.Title).FontSize(ty.Title).FontWeight(600).SingleLine()
				ui.Text(c, s.Artist).FontSize(ty.Sub).TextColor(th.Color.Muted).SingleLine()
			})
		})
		if len(lyrics) == 0 {
			ui.Column(c).Padding(th.Space(10), 0).Gap(th.Space(2)).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icLyrics).Size(28, 28).TextColor(th.Color.Faint)
				ui.Text(c, locales.LyricsUnavailable()).FontSize(ty.Title).TextColor(th.Color.Muted).
					TextAlign(ui.Center)
			})
			return
		}
		ui.Column(c).Gap(th.Space(4)).Children(func() {
			for i, line := range lyrics {
				text := ui.Text(c, line).FontSize(ty.Section).FontWeight(700).LineHeight(1.3).LetterSpacing(-0.3)
				switch {
				case i < currentLyric:
					text.TextColor(th.Color.LyricSung)
				case i == currentLyric:
					text.TextColor(th.Color.Text)
				default:
					text.TextColor(th.Color.LyricAhead)
				}
			}
		})
		ui.Text(c, locales.LyricsPlaceholder()).FontSize(ty.Caption).TextColor(th.Color.Faint)
	})
}

func (b *barText) fromAlbum(album string) string {
	if b.from.stale(album) {
		b.from.v = fromAlbum.say1(album)
	}
	return b.from.v
}
