package main

import (
	"strings"
	"yt-music-player/internal/catalog"
	"yt-music-player/locales"

	"github.com/egoist/mygo/ui"
)

func (a *app) searchPage(c *ui.Context, w float32, query string) {
	query = strings.TrimSpace(query)
	last := a.loader.Found()
	shownQuery, found, title := query, last.Results, locales.SearchResults()
	switch {
	case query == "":
		found = catalog.Results{}
	case query == last.FailedQuery:
		loadFailed(c, locales.SearchFailed(query), last.Err, func() { a.loader.RetrySearch(query) })
		return
	case query != last.Query:
		// Keep the last results while new ones load, so the page doesn't
		// blink empty at every keystroke.
		if found.IsEmpty() {
			message(c, icSearch, a.typedText(query).searching, "", 1, nil)
			return
		}
		shownQuery, title = last.Query, locales.Searching()
	}
	if found.IsEmpty() {
		message(c, icSearch, a.typedText(query).none,
			locales.SearchNoneHint(), 1, nil)
		return
	}

	text := a.foundText(shownQuery, found.Albums)
	ui.Column(c).Padding(th.Space(1), th.Layout.Page, th.Space(12)).Gap(th.Space(9)).Children(func() {
		pageTitle(c, title, text.quoted)
		if len(found.Songs) > 0 {
			a.foundSongs(c, w, found.Songs)
		}
		if len(found.Albums) > 0 {
			a.foundAlbums(c, found.Albums, text.albums)
		}
	})
}

func (a *app) typedText(query string) *typedText {
	m := &a.memo.typed
	if m.stale(query) {
		m.v = typedText{searching: searchingFor.say1(query), none: searchNone.say1(query)}
	}
	return &m.v
}

func (a *app) foundText(query string, albums []catalog.Album) *foundText {
	m := &a.memo.found
	if m.stale(foundKey{query, keyOf(albums)}) {
		cards := make([]cardText, len(albums))
		for i := range albums {
			cards[i] = cardText{key: albums[i].ID, sub: albumByline(&albums[i])}
		}
		m.v = foundText{quoted: quoted.say1(query), albums: cards}
	}
	return &m.v
}

func (a *app) foundSongs(c *ui.Context, w float32, songs []catalog.Song) {
	layout := ui.Row(c).Gap(th.Space(6)).AlignItems(ui.Stretch)
	if w <= 760 {
		layout.Column()
	}
	layout.Children(func() {
		ui.Column(c).Gap(th.Space(3.5)).Width(340).Shrink(0).Children(func() {
			heading(c, locales.TopResult(), th.Type.Minor, 2)
			a.topResult(c, songs[0])
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(th.Space(1.5)).Children(func() {
			heading(c, locales.Songs(), th.Type.Minor, 2).Margin(0, 0, th.Space(2), 0)
			ui.Column(c).Margin(0, -th.Space(2)).FocusGroup(ui.Vertical).Children(func() {
				for _, s := range songs[:min(4, len(songs))] {
					a.songRow(c, s, songRowOpts{art: th.Layout.Sized().RowArt, showsAlbum: true, hasActions: true})
				}
			})
		})
	})
}

func (a *app) foundAlbums(c *ui.Context, albums []catalog.Album, cards []cardText) {
	ui.Column(c).Children(func() {
		sectionHeader(c, nil, "", locales.Albums(), "")
		shelf(c, "found-albums", func() {
			size := th.Layout.Sized().SmallCard
			for i := range albums {
				al := &albums[i]
				if mediaCard(c, cards[i].key, al.Art, al.Title, cards[i].sub, size, false).Clicked() {
					a.router.Push(albumPath(al.ID))
				}
			}
		})
	})
}

func (a *app) topResult(c *ui.Context, s catalog.Song) {
	ty := &th.Type
	card := ui.ButtonBase(c.Key("top")).Column().Padding(th.Space(5.5)).Gap(th.Space(4.5)).Radius(th.Radius.Dialog).
		AlignItems(ui.Start).Cursor(ui.CursorPointer).KeepFocus().Label(s.Title)
	hovered := raise(card)
	card.Transition(colorFade).Children(func() {
		coverArt(c, s.Art, 104, th.Radius.Card).Shadow(0, 10, 28, 0, th.Color.Shadow.Alpha(0.45))
		ui.Column(c).Gap(th.Space(2)).Children(func() {
			heading(c, s.Title, ty.Feature, 0).SingleLine()
			ui.Row(c).Gap(th.Space(2)).AlignItems(ui.Center).Children(func() {
				ui.Text(c, locales.SongBadge()).FontSize(ty.Badge).FontWeight(700).LetterSpacing(1).
					Padding(th.Space(0.75), th.Space(2)).Radius(th.Radius.Pill).Background(th.Color.Badge)
				ui.Text(c, s.Artist).FontSize(ty.Label).TextColor(th.Color.Muted).SingleLine()
			})
		})
		playBadge(c, 50, hovered).Absolute().Right(th.Space(5)).Bottom(th.Space(5))
	})
	if card.Clicked() {
		a.playSong(&s)
	}
}
