package main

import (
	"time"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/fetchstate"
	"yt-music-player/internal/loader"
	"yt-music-player/locales"

	"github.com/egoist/mygo/ui"
)

var homeChips = [...]func() string{
	locales.MoodAll, locales.MoodEnergize, locales.MoodRelax, locales.MoodFocus,
	locales.MoodWorkout, locales.MoodCommute, locales.MoodFeelGood, locales.MoodSleep,
}

func greeting(now time.Time) string {
	switch h := now.Hour(); {
	case h < 5:
		return locales.GreetingLate()
	case h < 12:
		return locales.GreetingMorning()
	case h < 18:
		return locales.GreetingAfternoon()
	default:
		return locales.GreetingEvening()
	}
}

// feedState reports a feed not asked for yet (as before the window attaches)
// as loading.
func (a *app) feedState(res fetchstate.Resource) fetchstate.Fetch {
	f := a.fetches.State(res)
	if f.State == fetchstate.Idle {
		f.State = fetchstate.Loading
	}
	return f
}

func (a *app) homePage(c *ui.Context, w float32) {
	ui.Column(c).Padding(th.Space(1), th.Layout.Page, th.Space(12)).Gap(th.Space(10)).Children(func() {
		ui.Column(c).Gap(th.Space(4.5)).Children(func() {
			pageTitle(c, greeting(c.Now()), locales.ListenNow())
			// Segmented: screen readers announce radio buttons, and it is one Tab stop.
			moods := ui.SegmentedBase(c.Key("moods"), &a.page.mood, len(homeChips))
			moods.Track.Gap(th.Space(2)).Wrap().Label(locales.Mood()).Children(func() {
				for i, name := range homeChips {
					chip(c, moods.Segment(i), name(), i == a.page.mood)
				}
			})
		})
		switch f := a.feedState(loader.HomeFeed); {
		case f.State == fetchstate.Failed:
			loadFailed(c, locales.HomeFailed(), f.Err, func() { a.loader.Retry(loader.HomeFeed) })
		case f.State == fetchstate.Loading:
			homeSkeleton(c, w)
		case !hasItems(a.loader.Home().Shelves):
			message(c, icSparkles, locales.HomeEmptyTitle(), locales.HomeEmptyDetail(), 2, nil)
		default:
			a.homeShelves(c, w)
		}
	})
}

func hasItems(shelves []catalog.Shelf) bool {
	for _, sh := range shelves {
		if len(sh.Items) > 0 {
			return true
		}
	}
	return false
}

func (a *app) homeShelves(c *ui.Context, w float32) {
	shelves := a.loader.Home().Shelves
	if shelfIndex, itemIndex, ok := featured(shelves); ok {
		a.hero(c, w, &shelves[shelfIndex], itemIndex)
	}
	cards := a.homeCards()
	playAll := true
	for i := range shelves {
		sh := &shelves[i]
		switch {
		case len(sh.Items) == 0:
		case songsOnly(sh.Items):
			a.songShelf(c, w, sh, playAll)
			playAll = false
		default:
			a.cardShelf(c, sh, cards[i])
		}
	}
}

// homeCards is indexed by shelf, then item.
func (a *app) homeCards() [][]cardText {
	shelves := a.loader.Home().Shelves
	if len(shelves) == 0 {
		return nil
	}
	m := &a.memo.home
	if m.stale(feedKey{&shelves[0], len(shelves)}) {
		m.v = make([][]cardText, len(shelves))
		for i, sh := range shelves {
			m.v[i] = make([]cardText, len(sh.Items))
			for j, it := range sh.Items {
				m.v[i][j] = cardText{key: it.Key(), sub: cardSub(it)}
			}
		}
	}
	return m.v
}

func cardSub(it catalog.Item) string {
	switch it.Kind {
	case catalog.ItemSong:
		return it.Song.Artist
	case catalog.ItemAlbum:
		if it.Album.Kind == "" {
			return it.Album.Artist
		}
		return albumKind(it.Album.Kind) + " · " + it.Album.Artist
	case catalog.ItemPlaylist:
		return it.Playlist.Owner
	}
	return locales.Artist()
}

func songsOnly(items []catalog.Item) bool {
	for _, it := range items {
		if it.Kind != catalog.ItemSong {
			return false
		}
	}
	return true
}

func (a *app) songShelf(c *ui.Context, w float32, sh *catalog.Shelf, playAll bool) {
	ui.Column(c).Children(func() {
		action := ""
		if playAll {
			action = locales.PlayAll()
		}
		if sectionHeader(c, "play-all", sh.Strapline, sh.Title, action) {
			ids := make([]string, len(sh.Items))
			songs := make([]catalog.Song, len(sh.Items))
			for i, it := range sh.Items {
				ids[i], songs[i] = it.Song.ID, it.Song
			}
			a.client.PlayAll(ids, songs)
		}
		cols := columnsFor(w, 300, 1, 3)
		grid := ui.Grid(c).Columns(cols).GapX(th.Space(4)).GapY(th.Space(0.5)).Margin(0, -th.Space(2)).
			FocusGroup(ui.Horizontal | ui.Vertical)
		grid.Children(func() {
			for _, it := range sh.Items[:min(len(sh.Items), cols*4)] {
				a.songRow(c, it.Song, songRowOpts{art: th.Layout.Sized().RowArt, showsAlbum: true, hasActions: true})
			}
		})
	})
}

func (a *app) cardShelf(c *ui.Context, sh *catalog.Shelf, cards []cardText) {
	ui.Column(c).Children(func() {
		sectionHeader(c, nil, sh.Strapline, sh.Title, "")
		size := th.Layout.Sized().Card
		shelf(c, "cards", func() {
			for i := range sh.Items {
				a.feedCard(c, &sh.Items[i], cards[i], size)
			}
		})
	})
}

// feedCard opens nothing for artists until artist pages exist.
func (a *app) feedCard(c *ui.Context, it *catalog.Item, text cardText, size float32) {
	switch it.Kind {
	case catalog.ItemSong:
		s := &it.Song
		if mediaCard(c, text.key, s.Art, s.Title, text.sub, size, false).Clicked() {
			a.playSong(s)
		}
	case catalog.ItemAlbum:
		album := &it.Album
		if mediaCard(c, text.key, album.Art, album.Title, text.sub, size, false).Clicked() {
			a.router.Push(albumPath(album.ID))
		}
	case catalog.ItemPlaylist:
		playlist := &it.Playlist
		if mediaCard(c, text.key, playlist.Art, playlist.Name, text.sub, size, false).Clicked() {
			a.router.Push(playlistPath(playlist.ID))
		}
	case catalog.ItemArtist:
		mediaCard(c, text.key, it.Artist.Art, it.Artist.Name, text.sub, size*0.82, true)
	}
}

func featured(shelves []catalog.Shelf) (shelfIndex, itemIndex int, ok bool) {
	for i, sh := range shelves {
		for j, it := range sh.Items {
			if it.Kind == catalog.ItemPlaylist {
				return i, j, true
			}
		}
	}
	return 0, 0, false
}

func (a *app) hero(c *ui.Context, w float32, sh *catalog.Shelf, itemIndex int) {
	playlist := sh.Items[itemIndex].Playlist
	cv := coverOf(playlist.Art)
	col := &th.Color
	banner := ui.Row(c).Height(248).Radius(th.Radius.Hero).AlignItems(ui.Stretch).
		LinearGradient(ui.LinearGradient{From: cv.from, To: cv.to, Angle: 110, Oklab: true})
	pt := &a.paints
	pt.hero = cv
	if pt.heroFn == nil {
		pt.heroFn = func(p *ui.Painter, r ui.Rect) {
			radius, shade := th.Radius.Hero, th.Color.Shadow
			waves := cover{from: ui.Transparent, to: ui.Transparent, ink: pt.hero.ink.Alpha(0.5), motif: motifWaves}
			waves.paint(p, r, radius)
			// Keeps white text legible on any cover.
			p.FillGradient(r, ui.LinearGradient{From: shade.Alpha(0.55), To: shade.Alpha(0), Angle: 90, End: 0.7}, radius)
		}
	}
	banner.Draw(pt.heroFn)
	banner.Children(func() {
		info := ui.Column(c).Grow(1).MinWidth(0).Justify(ui.Center).
			Padding(th.Space(8), th.Space(9)).Gap(th.Space(2.5))
		info.Children(func() {
			ui.Row(c).Gap(th.Space(2)).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icSparkles).Size(14, 14).TextColor(col.Light.Alpha(0.85))
				eyebrow(c, sh.Title).TextColor(col.Light.Alpha(0.85))
			})
			titleSize := th.Type.Hero
			if w < 900 {
				titleSize = th.Type.HeroNarrow
			}
			heading(c, playlist.Name, titleSize, 2).TextColor(col.Light).SingleLine()
			if playlist.Owner != "" {
				ui.Text(c, playlist.Owner).FontSize(th.Type.Title).LineHeight(1.45).TextColor(col.Light.Alpha(0.78)).
					MaxWidth(420).MaxLines(2)
			}
			ui.Row(c).Gap(th.Space(2.5)).Margin(th.Space(2.5), 0, 0, 0).Children(func() {
				if pill(c, "hero-play", icPlay, locales.Play(), pillLight).Clicked() {
					a.playPlaylist(playlist.ID)
				}
				if pill(c, "hero-open", icQueue, locales.Open(), pillGlass).Clicked() {
					a.router.Push(playlistPath(playlist.ID))
				}
			})
		})
		if w > 600 {
			heroStack(c, sh.Items, itemIndex)
		}
	})
}

// heroStack fans the featured cover over the next two of its shelf on hover.
// Sleeves fan as poses, which only paint, except a sleeve that clips: a
// posed clip draws through a mask texture, so that one moves in the layout.
func heroStack(c *ui.Context, items []catalog.Item, itemIndex int) {
	var behind [2]catalog.Art
	behindCount := 0
	for _, it := range items[itemIndex+1:] {
		if art := artOf(it); art != "" && behindCount < len(behind) {
			behind[behindCount] = art
			behindCount++
		}
	}
	box := ui.Box(c).Width(300).Shrink(0)
	hovered := boolToFloat(box.Hovered())
	// Animated lazily: only a clipping sleeve needs it, and it builds every frame.
	fan := float32(-1)
	box.Children(func() {
		sleeve := func(art catalog.Art, size, x, y, dx, dy float32) {
			sleeveArt := coverArt(c, art, size, size, th.Radius.Sleeve)
			if coverOf(art).clips(th.Radius.Sleeve, size) {
				if fan < 0 {
					fan = box.Animate("fan", hovered, fanMotion.Duration)
				}
				x, y = x+dx*fan, y+dy*fan
			} else {
				sleeveArt.AnimatePose(ui.Pose{X: dx * hovered, Y: dy * hovered}, fanMotion)
			}
			sleeveArt.Absolute().Left(x).Top(y).Shadow(0, 18, 40, 0, th.Color.Shadow.Alpha(0.45))
		}
		if behindCount == 2 {
			sleeve(behind[1], 132, 150, 62, 16, 0)
		}
		if behindCount >= 1 {
			sleeve(behind[0], 132, 18, 62, -16, 0)
		}
		sleeve(artOf(items[itemIndex]), 164, 68, 42, 0, -6)
	})
}

var fanMotion = ui.Animation{Duration: 260 * time.Millisecond}

func artOf(it catalog.Item) catalog.Art {
	switch it.Kind {
	case catalog.ItemSong:
		return it.Song.Art
	case catalog.ItemAlbum:
		return it.Album.Art
	case catalog.ItemPlaylist:
		return it.Playlist.Art
	}
	return it.Artist.Art
}

// homeSkeleton mirrors the feed's shape: the hero, a song shelf, a card shelf.
func homeSkeleton(c *ui.Context, w float32) {
	size := th.Layout.Sized()
	loadingStatus(c).Margin(0, 0, -th.Space(6), 0)
	bone(c, 0, 248, th.Radius.Hero)
	ui.Column(c).Gap(th.Space(4)).Children(func() {
		bone(c, 120, 11, 5)
		bone(c, 200, 22, 8)
		cols := columnsFor(w, 300, 1, 3)
		ui.Grid(c).Columns(cols).GapX(th.Space(4)).GapY(th.Space(0.5)).Children(func() {
			for range cols * 4 {
				ui.Row(c).Padding(0, 0, th.Space(4.5), 0).Gap(th.Space(3.5)).AlignItems(ui.Center).Children(func() {
					bone(c, size.RowArt, size.RowArt, th.Radius.Thumb)
					boneLines(c, 160, 110)
				})
			}
		})
	})
	cardsSkeleton(c, w, size.Card)
}

func cardsSkeleton(c *ui.Context, w, size float32) {
	ui.Column(c).Gap(th.Space(4)).Children(func() {
		bone(c, 220, 22, 8)
		ui.Row(c).Gap(th.Space(5)).ClipX().Children(func() {
			for range int(w/(size+th.Space(5))) + 1 {
				boneCard(c, size, size, 0.5)
			}
		})
	})
}
