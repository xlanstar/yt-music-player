package main

import (
	"fmt"
	"slices"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/fetchstate"
	"yt-music-player/internal/session"
	"yt-music-player/locales"

	"github.com/egoist/mygo/ui"
)

type collectionKind uint8

const (
	noCollection collectionKind = iota
	albumCollection
	playlistCollection
	likedMusicCollection // a playlist that says how likes fill it
)

type collection struct {
	kind                        collectionKind
	eyebrow, title, owner, meta string
	art                         catalog.Art
	songs                       []catalog.Song
}

func (a *app) likedCollection() collection {
	owner := a.session.Account().Name
	if owner == "" {
		owner = locales.You()
	}
	m := &a.memo.collection
	if m.stale(collectionKey{session.LikedRes, a.cat.Gen(), a.session.LikesGen(), owner}) {
		songs := a.session.LikedSongs()
		m.v = collection{
			kind: likedMusicCollection, eyebrow: locales.AutoPlaylist(), title: locales.LikedMusic(), owner: owner,
			meta: totalLength(songs), art: artLiked, songs: songs,
		}
	}
	return m.v
}

func (a *app) itemPage(c *ui.Context, w float32, r *ui.Route, res fetchstate.Resource) {
	label, failed := locales.Album(), locales.AlbumFailed()
	if res.Kind == fetchstate.Playlist {
		label, failed = locales.Playlist(), locales.PlaylistFailed()
	}
	f := a.fetches.Get(res)
	switch f.State {
	case fetchstate.Missing:
		notFound(c)
		return
	case fetchstate.Failed:
		r.Title(label)
		loadFailed(c, failed, f.Err, func() { a.loader.Retry(res) })
		return
	}
	col := a.cachedCollection(res)
	// Feeds list albums and playlists without their songs: don't show one as
	// empty while its songs load.
	isLoading := f.State == fetchstate.Idle || f.State == fetchstate.Loading
	if col.kind == noCollection || len(col.songs) == 0 && isLoading {
		r.Title(label)
		collectionSkeleton(c, w, label)
		return
	}
	r.Title(col.title)
	a.collectionPage(c, w, col)
}

// cachedCollection is noCollection when the catalog lacks res.
func (a *app) cachedCollection(res fetchstate.Resource) collection {
	m := &a.memo.collection
	if !m.stale(collectionKey{res: res, music: a.cat.Gen()}) {
		return m.v
	}
	m.v = collection{}
	if res.Kind == fetchstate.Album {
		if al, ok := a.cat.Album(res.ID); ok {
			songs := a.cat.AlbumTracks(al.ID)
			m.v = collection{
				kind: albumCollection, eyebrow: albumKind(al.Kind), title: al.Title, owner: al.Artist,
				meta: fmt.Sprintf("· %d %s", al.Year, totalLength(songs)), art: al.Art, songs: songs,
			}
		}
		return m.v
	}
	if pl, ok := a.cat.Playlist(res.ID); ok {
		songs := a.cat.SongsByID(pl.Songs)
		m.v = collection{
			kind: playlistCollection, eyebrow: locales.Playlist(), title: pl.Name, owner: pl.Owner,
			meta: totalLength(songs), art: pl.Art, songs: songs,
		}
	}
	return m.v
}

// albumKind says an album's release type in the app's language.
func albumKind(k catalog.Release) string {
	switch k {
	case catalog.ReleaseAlbum:
		return locales.Album()
	case catalog.ReleaseEP:
		return locales.AlbumEP()
	case catalog.ReleaseSingle:
		return locales.AlbumSingle()
	}
	return string(k)
}

func (a *app) collectionPage(c *ui.Context, w float32, col collection) {
	artSize := collectionArt(w)
	ty := &th.Type
	// An album's art and title would repeat on every row.
	a.songList(c, nil, col.songs, col.kind != albumCollection, w, th.Space(2), func() {
		ui.Column(c).Padding(0, rowBleed).Children(func() {
			ui.Row(c).Gap(th.Space(7)).AlignItems(ui.End).Children(func() {
				coverArt(c, col.art, artSize, th.Radius.Large).Shadow(0, 24, 60, -8, th.Color.Shadow.Alpha(0.6))
				ui.Column(c).Grow(1).MinWidth(0).Gap(th.Space(2.5)).Children(func() {
					eyebrow(c, col.eyebrow)
					heading(c, col.title, ty.Display, 1).MaxLines(2).LineHeight(1.05)
					ui.Row(c).Gap(th.Space(2)).AlignItems(ui.Center).Margin(th.Space(1), 0, 0, 0).Children(func() {
						ui.Avatar(c, col.owner, nil).Size(22, 22)
						ui.Text(c, col.owner).FontSize(ty.Label).FontWeight(600).SingleLine()
						ui.Text(c, col.meta).FontSize(ty.Label).TextColor(th.Color.Muted).SingleLine()
					})
				})
			})
			a.collectionActions(c, col.songs)
			if len(col.songs) == 0 {
				ui.Column(c).Padding(th.Space(12), 0).Gap(th.Space(1.5)).AlignItems(ui.Center).Children(func() {
					heading(c, locales.NothingHereYet(), ty.Minor, 2)
					if col.kind == likedMusicCollection {
						ui.Text(c, locales.LikedEmpty()).TextColor(th.Color.Muted)
					}
				})
			}
		})
	})
}

func collectionArt(w float32) float32 {
	if w < 640 {
		return 168
	}
	return 220
}

func collectionSkeleton(c *ui.Context, w float32, label string) {
	artSize := collectionArt(w)
	ui.Column(c).Grow(1).Padding(th.Space(2), th.Layout.Page, th.Space(12)).Children(func() {
		ui.Row(c).Gap(th.Space(7)).AlignItems(ui.End).Children(func() {
			bone(c, artSize, artSize, th.Radius.Large)
			ui.Column(c).Grow(1).MinWidth(0).Gap(th.Space(3.5)).Children(func() {
				eyebrow(c, label)
				bone(c, max(min(w-artSize-th.Space(7), 420), 120), 48, 10)
				bone(c, 220, 16, 8)
			})
		})
		ui.Row(c).Height(56).Margin(th.Space(7), 0, th.Space(3), 0).AlignItems(ui.Center).
			Children(func() { loadingStatus(c) })
		for range 6 {
			row := ui.Row(c).Height(th.Layout.Sized().Row).Padding(0, th.Space(3)).Gap(th.Space(4)).
				AlignItems(ui.Center)
			row.Children(func() {
				bone(c, numberColumn, 14, 7)
				boneLines(c, 200, 120)
				bone(c, 36, 12, 6)
			})
		}
	})
}

func (a *app) collectionActions(c *ui.Context, songs []catalog.Song) {
	holdsCurrent := a.holdsCurrent(songs)
	row := ui.Row(c).Gap(th.Space(2.5)).AlignItems(ui.Center).Margin(th.Space(7), 0, th.Space(3), 0).
		FocusGroup(ui.Horizontal)
	row.Children(func() {
		icon, label := icPlay, locales.Play()
		if holdsCurrent && a.isPlaying() {
			icon, label = icPause, locales.Pause()
		}
		play := roundPlayButton(c, "play-all", icon, label, btnPlayAll, th.Color.Accent, th.Color.AccentText).
			Shadow(0, 8, 24, 0, th.Color.Accent.Alpha(0.35))
		if play.Hovered() {
			play.Background(th.Color.Accent.Mix(th.Color.Light, 0.08))
		}
		if play.Clicked() && len(songs) > 0 {
			if holdsCurrent {
				a.client.Toggle()
			} else {
				a.playSong(&songs[0])
			}
		}
		ui.Box(c).Width(th.Space(1.5))
		a.shuffleButton(c, btnAction)
		iconButton(c, "save", icPlus, locales.SaveToLibrary(), btnAction, iconOpts{})
		iconButton(c, "download", icDownload, locales.Download(), btnAction, iconOpts{})
		iconButton(c, "more", icMore, locales.More(), btnAction, iconOpts{})
	})
}

// holdsCurrent is memoized, as a scan of thousands of songs would run
// every frame.
func (a *app) holdsCurrent(songs []catalog.Song) bool {
	current := a.currentID()
	key := holdsKey{n: len(songs), current: current}
	if len(songs) > 0 {
		key.first = &songs[0]
	}
	m := &a.memo.holds
	if m.stale(key) {
		m.v = slices.ContainsFunc(songs, func(s catalog.Song) bool { return s.ID == current })
	}
	return m.v
}

// rowBleed is how far rows reach past the page padding, so their hover fill
// surrounds cover and text that align with the page.
const rowBleed = 12

// pageHeaderKey can never equal a song ID, a string, and MyGo hashes an
// int64 as it is: a struct key would go through fmt.Sprintf every frame.
const pageHeaderKey int64 = -1

// pageList is a page as one list, the header then count rows, building only
// those in view. Rows follow their items by key when key is set. The router
// keeps the scroll position unless state does.
func pageList(
	c *ui.Context,
	state *ui.ListState,
	top float32,
	count int,
	key func(i int) any,
	header func(),
	row func(i int),
) {
	list := ui.List(c, state, count+1).Grow(1).Padding(top, th.Layout.Page-rowBleed, th.Space(12)).
		ScrollbarInsets(0, 0, th.Layout.PanelRadius)
	if key != nil {
		list.ItemKey(func(i int) any {
			if i == 0 {
				return pageHeaderKey
			}
			return key(i - 1)
		})
	}
	list.Rows(func(r ui.ListRow) {
		if r.Index == 0 {
			ui.Column(c).Children(header)
			return
		}
		row(r.Index - 1)
	})
}

func (a *app) songList(
	c *ui.Context,
	state *ui.ListState,
	songs []catalog.Song,
	showAlbum bool,
	w, top float32,
	header func(),
) {
	albumWidth := min(max(w*0.24, 120), 260)
	opts := songRowOpts{isInTable: true, hasActions: true}
	if showAlbum {
		opts.art, opts.albumWidth = 40, albumWidth
	}
	pageList(c, state, top, len(songs), func(i int) any { return a.rowText(&songs[i]).key }, func() {
		header()
		if len(songs) > 0 {
			tableHead(c, showAlbum, albumWidth)
		}
	}, func(i int) {
		opts.index = i
		a.songRow(c, songs[i], opts)
	})
}

func tableHead(c *ui.Context, showAlbum bool, albumWidth float32) {
	ui.Row(c).Height(36).Padding(0, th.Space(3)).Gap(th.Space(4)).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(th.Color.Line).Margin(0, 0, th.Space(2), 0).
		TextColor(th.Color.Faint).FontSize(th.Type.Small).FontWeight(600).Children(func() {
		ui.Text(c, "#").Width(numberColumn).TextAlign(ui.Center)
		ui.Text(c, locales.ColumnTitle()).LetterSpacing(1).Grow(1)
		if showAlbum {
			ui.Text(c, locales.ColumnAlbum()).LetterSpacing(1).Width(albumWidth)
		}
		ui.Box(c).Width(likeColumn)
		ui.Row(c).Width(durationColumn).Justify(ui.End).Children(func() { ui.Icon(c, icClock).Size(15, 15) })
	})
}
