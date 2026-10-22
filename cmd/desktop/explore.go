package main

import (
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/fetchstate"
	"yt-music-player/internal/loader"
	"yt-music-player/locales"

	"github.com/egoist/mygo/ui"
)

func (a *app) explorePage(c *ui.Context, w float32) {
	ui.Column(c).Padding(th.Space(1), th.Layout.Page, th.Space(12)).Gap(th.Space(10)).Children(func() {
		ui.Column(c).Gap(th.Space(4.5)).Children(func() {
			heading(c, locales.NavExplore(), th.Type.Page, 1)
			ui.Row(c).Gap(th.Space(3.5)).FocusGroup(ui.Horizontal).Children(func() {
				col := &th.Color
				tints := [len(exploreTiles)]ui.Color{col.Releases, col.Charts, col.Moods}
				for i := range exploreTiles {
					exploreTile(c, &exploreTiles[i], tints[i])
				}
			})
		})
		e := a.loader.Explore()
		isEmpty := len(e.Moods) == 0 && len(e.NewReleases) == 0 && len(e.Trending) == 0
		switch f := a.feedState(loader.ExploreRes); {
		case f.State == fetchstate.Failed:
			loadFailed(c, locales.ExploreFailed(), f.Err, func() { a.loader.Retry(loader.ExploreRes) })
		case f.State == fetchstate.Loading:
			exploreSkeleton(c, w)
		case isEmpty:
			message(c, icCompass, locales.ExploreEmptyTitle(), locales.ExploreEmptyDetail(), 2, nil)
		default:
			a.exploreSections(c, w, e)
		}
	})
}

func (a *app) exploreSections(c *ui.Context, w float32, e *catalog.Explore) {
	size := th.Layout.Sized()
	if len(e.Moods) > 0 {
		ui.Column(c).Children(func() {
			sectionHeader(c, nil, "", locales.MoodsAndGenres(), "")
			keys := a.moodKeys()
			grid := ui.Grid(c).Columns(moodColumns(w)).Gap(th.Space(3)).FocusGroup(ui.Horizontal | ui.Vertical)
			grid.Children(func() {
				for i := range e.Moods {
					moodTile(c, keys[i], &e.Moods[i])
				}
			})
		})
	}
	if len(e.NewReleases) > 0 {
		ui.Column(c).Children(func() {
			sectionHeader(c, nil, "", locales.NewAlbumsAndSingles(), "")
			cards := a.releaseCards()
			shelf(c, "new", func() {
				for i, al := range e.NewReleases {
					a.feedCard(c, &catalog.Item{Kind: catalog.ItemAlbum, Album: al}, cards[i], size.SmallCard)
				}
			})
		})
	}
	if len(e.Trending) > 0 {
		ui.Column(c).Children(func() {
			sectionHeader(c, nil, locales.ThisWeek(), locales.Trending(), "")
			grid := ui.Grid(c).Columns(trendingColumns(w)).GapX(th.Space(6)).GapY(th.Space(0.5)).
				Margin(0, -th.Space(2)).FocusGroup(ui.Horizontal | ui.Vertical)
			grid.Children(func() {
				for rank, s := range e.Trending {
					a.songRow(c, s, songRowOpts{isRanked: true, index: rank, art: size.RowArt})
				}
			})
		})
	}
}

func (a *app) releaseCards() []cardText {
	releases := a.loader.Explore().NewReleases
	if len(releases) == 0 {
		return nil
	}
	m := &a.memo.releases
	if m.stale(feedKey{&releases[0], len(releases)}) {
		m.v = make([]cardText, len(releases))
		for i, al := range releases {
			m.v[i] = cardText{key: al.ID, sub: cardSub(catalog.Item{Kind: catalog.ItemAlbum, Album: al})}
		}
	}
	return m.v
}

func (a *app) moodKeys() []any {
	moods := a.loader.Explore().Moods
	m := &a.memo.moods
	if m.stale(keyOf(moods)) {
		m.v = make([]any, len(moods))
		for i, mood := range moods {
			m.v[i] = mood.Key()
		}
	}
	return m.v
}

func moodColumns(w float32) int     { return columnsFor(w, 190, 2, 6) }
func trendingColumns(w float32) int { return columnsFor(w, 380, 1, 2) }

func exploreSkeleton(c *ui.Context, w float32) {
	size := th.Layout.Sized()
	loadingStatus(c).Margin(0, 0, -th.Space(6), 0)
	ui.Column(c).Gap(th.Space(4)).Children(func() {
		bone(c, 200, 22, 8)
		cols := moodColumns(w)
		ui.Grid(c).Columns(cols).Gap(th.Space(3)).Children(func() {
			for range cols * 2 {
				bone(c, 0, 52, th.Radius.Control)
			}
		})
	})
	cardsSkeleton(c, w, size.SmallCard)
	ui.Column(c).Gap(th.Space(4)).Children(func() {
		bone(c, 160, 22, 8)
		ui.Grid(c).Columns(trendingColumns(w)).GapX(th.Space(6)).GapY(th.Space(4.5)).Children(func() {
			for range 8 {
				ui.Row(c).Gap(th.Space(3.5)).AlignItems(ui.Center).Children(func() {
					bone(c, 36, 18, 6)
					bone(c, size.RowArt, size.RowArt, th.Radius.Thumb)
					boneLines(c, 160, 110)
				})
			}
		})
	})
}

// exploreTiles head Explore; they open nothing until their pages exist.
var exploreTiles = [...]navTarget{
	{key: "New releases", label: locales.NewReleases, icon: icDisc},
	{key: "Charts", label: locales.Charts, icon: icTrending},
	{key: "Moods & genres", label: locales.MoodsAndGenres, icon: icGrid},
}

func exploreTile(c *ui.Context, t *navTarget, tint ui.Color) {
	icon, label := t.icon, t.label()
	tile := ui.ButtonBase(c.Key(t.key)).Grow(1).Basis(0).Height(76).Padding(0, th.Space(4.5)).Gap(th.Space(3.5)).
		Radius(th.Radius.Large).AlignItems(ui.Center).Justify(ui.Start).Cursor(ui.CursorPointer).KeepFocus()
	raise(tile)
	tile.Transition(colorFade).Children(func() {
		ui.Box(c).Size(42, 42).Radius(th.Radius.Card).Center().Background(tint.Alpha(0.16)).Children(func() {
			ui.Icon(c, icon).Size(20, 20).TextColor(tint)
		})
		ui.Text(c, label).FontSize(th.Type.Tile).FontWeight(600).SingleLine().Shrink(1).MinWidth(0)
	})
}

// moodTile opens nothing until mood pages exist.
func moodTile(c *ui.Context, key any, m *catalog.Mood) {
	tile := ui.ButtonBase(c.Key(key)).Height(52).Padding(0, th.Space(4.5)).Radius(th.Radius.Control).
		AlignItems(ui.Center).Justify(ui.Start).Clip().Cursor(ui.CursorPointer).KeepFocus()
	raise(tile)
	tile.Children(func() {
		ui.Box(c).Absolute().Left(0).Top(0).Bottom(0).Width(5).Background(oklch(m.Tint.L, m.Tint.C, m.Tint.H))
		ui.Text(c, m.Name).FontSize(th.Type.Title).FontWeight(600).SingleLine()
	})
}
