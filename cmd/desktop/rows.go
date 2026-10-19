package main

import (
	"yt-music-player/internal/catalog"
	"yt-music-player/locales"

	"github.com/egoist/mygo/ui"
)

type songRowOpts struct {
	index      int     // position shown in a table or ranked, from 0
	art        float32 // cover size, 0 for none
	albumWidth float32 // album column width, 0 for none
	// isInTable lays the row out in songList's columns.
	isInTable bool
	// isRanked leads with a chart position and its trend.
	isRanked   bool
	showsAlbum bool // "artist · album" under the title, not just the artist
	// hasActions shows Like and More on hover or focus; liked songs keep Like.
	hasActions bool
	isCompact  bool // smaller type, for the narrow queue
}

// songRow puts the song button's actions beside it, not inside:
// assistive technology reads a button as one leaf, hiding buttons nested
// in it.
func (a *app) songRow(c *ui.Context, s catalog.Song, opts songRowOpts) {
	isCurrent := s.ID == a.currentID()
	text := a.rowText(&s)
	row := ui.Row(c.Key(text.key)).Radius(th.Radius.Control).AlignItems(ui.Center).Cursor(ui.CursorPointer)
	gap := th.Space(3.5)
	switch {
	case opts.isInTable:
		row.Height(th.Layout.Sized().Row).Padding(0, th.Space(3))
		gap = th.Space(4)
	case opts.isCompact:
		row.Padding(th.Space(1.75), th.Space(2))
		gap = th.Space(3)
	default:
		row.Padding(th.Space(2))
	}
	row.Gap(gap)
	// Focus inside counts as hover, so the actions built only then stay
	// reachable with Tab.
	hovered := row.Hovered() || row.FocusWithin()
	switch {
	case hovered:
		row.Background(th.Color.RowHover)
	case isCurrent && opts.isInTable:
		row.Background(th.Color.RowCurrent)
	}
	row.Children(func() {
		songButton := ui.ButtonBase(c.Key("song")).Grow(1).Shrink(1).MinWidth(0).AlignSelf(ui.Stretch).
			Gap(gap).Justify(ui.Start).Label(text.label)
		songButton.Children(func() {
			switch {
			case opts.isInTable:
				a.trackNumber(c, opts.index, isCurrent, hovered)
				ui.Row(c).Grow(1).MinWidth(0).Gap(th.Space(3.5)).AlignItems(ui.Center).Children(func() {
					if opts.art > 0 {
						coverArt(c, s.Art, opts.art, opts.art, th.Radius.Thumb)
					}
					a.songTitle(c, s, text, opts, isCurrent)
				})
			default:
				if opts.isRanked {
					chartRank(c, opts.index)
				}
				if opts.art > 0 {
					a.thumb(c, s, opts.art, th.Radius.Thumb, hovered)
				}
				a.songTitle(c, s, text, opts, isCurrent)
			}
			if opts.albumWidth > 0 {
				ui.Text(c, s.Album).FontSize(th.Type.Detail).TextColor(th.Color.Muted).Width(opts.albumWidth).SingleLine()
			}
		})
		if songButton.Clicked() {
			a.playSong(&s)
		}
		a.songActions(c, s, opts, hovered)
	})
	// Clicks on the row's padding and duration miss the button.
	if row.Clicked() {
		a.playSong(&s)
	}
}

func (a *app) songTitle(c *ui.Context, s catalog.Song, text *rowText, opts songRowOpts, isCurrent bool) {
	ty := &th.Type
	titleSize, subSize, gap := ty.Title, ty.Sub, th.Space(0.75)
	if opts.isCompact {
		titleSize, subSize, gap = ty.Label, ty.Small, th.Space(0.5)
	}
	ui.Column(c).Grow(1).MinWidth(0).Gap(gap).Children(func() {
		title := ui.Text(c, s.Title).FontSize(titleSize).FontWeight(600).SingleLine()
		if isCurrent {
			title.TextColor(th.Color.Accent)
		}
		line := s.Artist
		if opts.showsAlbum {
			line = text.sub
		}
		ui.Text(c, line).FontSize(subSize).TextColor(th.Color.Muted).SingleLine()
	})
}

// songActions builds what follows the song button: Like and More where
// enabled, then the duration, which More replaces on hover outside tables.
func (a *app) songActions(c *ui.Context, s catalog.Song, opts songRowOpts, hovered bool) {
	like := func() {
		if opts.hasActions && (hovered || a.session.Liked(s.ID)) {
			a.likeButton(c, s.ID, 32)
		}
	}
	if opts.isInTable {
		ui.Row(c).Width(36).Justify(ui.Center).Children(like)
		ui.Text(c, clock(s.Seconds)).FontSize(th.Type.Detail).TextColor(th.Color.Muted).FontFeatures("tnum").
			Width(48).TextAlign(ui.End)
		return
	}
	like()
	if opts.hasActions && hovered {
		iconButton(c, "more", icMore, locales.More(), 32, 18, iconOpts{})
		return
	}
	size := th.Type.Sub
	if opts.isCompact {
		size = th.Type.Small
	}
	ui.Text(c, clock(s.Seconds)).FontSize(size).TextColor(th.Color.Faint).FontFeatures("tnum").
		Width(40).TextAlign(ui.End)
}

func (a *app) trackNumber(c *ui.Context, index int, isCurrent, hovered bool) {
	ui.Row(c).Width(28).Justify(ui.Center).Children(func() {
		switch {
		case hovered && isCurrent && a.isPlaying():
			ui.Icon(c, icPause).Size(14, 14).TextColor(th.Color.Text)
		case hovered:
			ui.Icon(c, icPlay).Size(14, 14).TextColor(th.Color.Text)
		case isCurrent:
			eqBars(c, a.eqDancing(), th.Color.Accent, 14)
		default:
			ui.Text(c, numeral(index+1)).FontSize(th.Type.Label).TextColor(th.Color.Muted).FontFeatures("tnum")
		}
	})
}

// chartRank shows placeholder trends that cycle until the API reports
// real ones.
func chartRank(c *ui.Context, index int) {
	ui.Text(c, numeral(index+1)).FontSize(th.Type.Minor).FontWeight(700).TextColor(th.Color.Muted).FontFeatures("tnum").
		Width(26).TextAlign(ui.Center)
	trend, col := "▲", th.Color.Rise
	switch index % 3 {
	case 1:
		trend, col = "▼", th.Color.Fall
	case 2:
		trend, col = "–", th.Color.Faint
	}
	ui.Text(c, trend).FontSize(th.Type.Micro).TextColor(col).Width(10)
}
