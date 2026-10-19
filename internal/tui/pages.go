package tui

import (
	"strconv"
	"strings"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/fetchstate"
	"yt-music-player/internal/session"
	"yt-music-player/locales"
)

// itemKind is what a list's row is.
type itemKind uint8

const (
	itemSong itemKind = iota + 1
	itemAlbum
	itemPlaylist
	itemLiked
	itemPlayAll
	itemHeading
)

// item is a list's row; only the fields of its kind are set.
type item struct {
	song     catalog.Song
	album    catalog.Album
	playlist catalog.Playlist
	// text is a heading's, or Liked music's count.
	text string
	kind itemKind
	// isCurrent marks the queue's current song.
	isCurrent bool
}

// page is what the body shows: header lines, then a message or a list.
type page struct {
	header []string
	// art is the cover beside the header's first two lines.
	art catalog.Art

	// A message, when title is set; enter calls retry, if set.
	title, detail, hint string
	retry               func()

	// A list of n items.
	list *listState
	n    int
	at   func(i int) item
	// songs are a collection's, for Play all.
	songs func() []catalog.Song
}

func (p *page) selectable(i int) bool { return p.at(i).kind != itemHeading }

// selected returns the item under the cursor, if the page lists any.
func (p *page) selected() (item, bool) {
	if p.title != "" || p.list == nil || p.n == 0 {
		return item{}, false
	}
	i := min(max(p.list.cursor, 0), p.n-1)
	return p.at(i), true
}

// page returns what the shown tab shows.
func (m *model) page() page {
	if o := m.opened[m.tab]; o.kind != 0 {
		return m.collectionPage(o)
	}
	switch m.tab {
	case tabSearch:
		return m.searchPage()
	case tabLibrary:
		return m.libraryPage()
	default:
		return m.queuePage()
	}
}

func (m *model) queuePage() page {
	queue := m.mirror.Queue()
	if len(queue) == 0 {
		return page{title: locales.TuiQueueEmpty(), detail: locales.TuiQueueEmptyHint()}
	}
	st := m.mirror.State()
	if m.queueFollows {
		m.queue.cursor = st.Index
	}
	return page{list: &m.queue, n: len(queue), at: func(i int) item {
		id := queue[i]
		song, ok := m.mirror.Song(id)
		if !ok {
			song = catalog.Song{ID: id, Title: id}
		}
		return item{kind: itemSong, song: song, isCurrent: i == st.Index && id == st.Current}
	}}
}

func (m *model) searchPage() page {
	p := page{header: []string{m.searchInput(), ""}}
	query := strings.TrimSpace(m.query)
	found := m.loader.Found()
	results := found.Results
	switch {
	case query == "":
		p.title, p.detail = locales.NavSearch(), locales.TuiSearchHint()
		return p
	case query == found.FailedQuery:
		p.title, p.detail = locales.SearchFailed(query), m.why(found.Err)
		p.hint = keyHint("enter", locales.Retry())
		p.retry = func() { m.loader.RetrySearch(query) }
		return p
	case query != found.Query:
		if results.IsEmpty() {
			p.title = locales.SearchingFor(query)
			return p
		}
		// The last results stay while new ones come, so the page does
		// not blink empty at every key.
		p.header[1] = styleMuted.Render(" " + locales.Searching())
	}
	if results.IsEmpty() {
		p.title, p.detail = locales.SearchNone(query), locales.SearchNoneHint()
		return p
	}
	songs, albums := results.Songs, results.Albums
	// The headings' places: songs after 0, albums after albumsAt.
	albumsAt := -1
	n := 0
	if len(songs) > 0 {
		n += 1 + len(songs)
	}
	if len(albums) > 0 {
		albumsAt = n
		n += 1 + len(albums)
	}
	p.list, p.n = &m.search, n
	p.at = func(i int) item {
		switch {
		case i == albumsAt:
			return item{kind: itemHeading, text: locales.Albums()}
		case albumsAt >= 0 && i > albumsAt:
			return item{kind: itemAlbum, album: albums[i-albumsAt-1]}
		case i == 0:
			return item{kind: itemHeading, text: locales.Songs()}
		default:
			return item{kind: itemSong, song: songs[i-1]}
		}
	}
	return p
}

// searchInput is the search page's input line.
func (m *model) searchInput() string {
	prompt := styleKey.Render(" / ")
	switch {
	case m.isTyping:
		return prompt + m.query + styleAccent.Render("█")
	case m.query == "":
		return prompt + styleMuted.Render(locales.SearchPlaceholder())
	}
	return prompt + m.query
}

func (m *model) libraryPage() page {
	if p, ok := m.accountPage(session.LibraryRes, locales.LibraryFailed()); !ok {
		return p
	}
	playlists := m.cat.LibraryPlaylists()
	albums := m.session.Library().Albums
	playlistsAt, albumsAt := -1, -1
	n := 1 // Liked music
	if len(playlists) > 0 {
		playlistsAt = n
		n += 1 + len(playlists)
	}
	if len(albums) > 0 {
		albumsAt = n
		n += 1 + len(albums)
	}
	liked := m.likedCount()
	return page{list: &m.library, n: n, at: func(i int) item {
		switch {
		case i == 0:
			return item{kind: itemLiked, text: liked}
		case i == playlistsAt:
			return item{kind: itemHeading, text: locales.SidebarPlaylists()}
		case i == albumsAt:
			return item{kind: itemHeading, text: locales.Albums()}
		case albumsAt >= 0 && i > albumsAt:
			return item{kind: itemAlbum, album: albums[i-albumsAt-1]}
		default:
			return item{kind: itemPlaylist, playlist: playlists[i-playlistsAt-1]}
		}
	}}
}

// accountPage returns the page of res, the session's, unless it loaded:
// signed out, failed (enter retries), or loading.
func (m *model) accountPage(res fetchstate.Resource, failed string) (page, bool) {
	f := m.states.State(res)
	switch f.State {
	case fetchstate.Loaded:
		return page{}, true
	case fetchstate.SignedOut:
		return m.signedOutPage(), false
	case fetchstate.Failed:
		return page{
			title: failed, detail: m.why(f.Err), hint: keyHint("enter", locales.Retry()),
			retry: func() { m.session.Retry(res) },
		}, false
	}
	return page{title: locales.Loading()}, false
}

func (m *model) signedOutPage() page {
	return page{
		title:  locales.TuiSignedOutTitle(),
		detail: locales.TuiSignedOutDetail(),
		hint:   keyHint("o", locales.TuiOpenDesktop()),
	}
}

// collectionPage shows an album, a playlist or Liked music: Play all,
// then its songs.
func (m *model) collectionPage(o opened) page {
	var title, subtitle string
	switch o.kind {
	case itemLiked:
		if p, ok := m.accountPage(session.LikedRes, locales.LikesFailed()); !ok {
			return p
		}
		title = locales.LikedMusic()
	default:
		kind, failed := fetchstate.Album, locales.AlbumFailed()
		if o.kind == itemPlaylist {
			kind, failed = fetchstate.Playlist, locales.PlaylistFailed()
		}
		res := fetchstate.Resource{Kind: kind, ID: o.id}
		switch f := m.states.State(res); f.State {
		case fetchstate.Loaded:
		case fetchstate.Missing:
			return page{title: locales.NotFoundTitle(), detail: locales.NotFoundDetail()}
		case fetchstate.Failed:
			return page{
				title: failed, detail: m.why(f.Err), hint: keyHint("enter", locales.Retry()),
				retry: func() { m.loader.Retry(res) },
			}
		default:
			return page{title: locales.Loading()}
		}
	}
	songs := m.collectionSongs(o)
	var art catalog.Art
	switch o.kind {
	case itemAlbum:
		album, _ := m.cat.Album(o.id)
		title, subtitle, art = album.Title, albumLine(album, songs), album.Art
	case itemPlaylist:
		pl, _ := m.cat.Playlist(o.id)
		title, subtitle, art = pl.Name, playlistLine(pl, songs), pl.Art
	default:
		subtitle = locales.AutoPlaylistSongs(len(songs))
	}
	header := []string{" " + styleBold.Render(title), " " + styleMuted.Render(subtitle), ""}
	if len(songs) == 0 {
		empty := locales.NothingHereYet()
		detail := ""
		if o.kind == itemLiked {
			detail = locales.LikedEmpty()
		}
		return page{header: header, art: art, title: empty, detail: detail}
	}
	return page{
		header: header,
		art:    art,
		list:   &m.collection[m.tab],
		n:      1 + len(songs),
		at: func(i int) item {
			if i == 0 {
				return item{kind: itemPlayAll}
			}
			return item{kind: itemSong, song: songs[i-1]}
		},
		songs: func() []catalog.Song { return songs },
	}
}

// albumLine is an album's line under its title: its kind, artist, year
// and length.
func albumLine(album catalog.Album, songs []catalog.Song) string {
	kind := locales.Album()
	switch album.Kind {
	case catalog.KindEP:
		kind = locales.AlbumEP()
	case catalog.KindSingle:
		kind = locales.AlbumSingle()
	}
	parts := []string{kind}
	if album.Artist != "" {
		parts = append(parts, album.Artist)
	}
	if album.Year > 0 {
		parts = append(parts, strconv.Itoa(album.Year))
	}
	return strings.Join(parts, " · ") + " " + length(songs)
}

// playlistLine is a playlist's line under its name.
func playlistLine(pl catalog.Playlist, songs []catalog.Song) string {
	by := locales.Playlist()
	if pl.Owner != "" {
		by = locales.PlaylistBy(pl.Owner)
	}
	return by + " " + length(songs)
}

// length says how many songs and minutes songs are.
func length(songs []catalog.Song) string {
	return locales.CollectionLength(len(songs), (catalog.TotalSeconds(songs)+30)/60)
}

// collectionSongs returns the songs of o, remade only when the catalog
// or the likes change.
func (m *model) collectionSongs(o opened) []catalog.Song {
	key := songsKey{o: o, catGen: m.cat.Gen(), likesGen: m.session.LikesGen()}
	if c := &m.memo.songs; !c.ok || c.key != key {
		var songs []catalog.Song
		switch o.kind {
		case itemAlbum:
			songs = m.cat.AlbumTracks(o.id)
		case itemPlaylist:
			pl, _ := m.cat.Playlist(o.id)
			songs = m.cat.SongsByID(pl.Songs)
		default:
			songs = m.session.LikedSongs()
		}
		*c = songsMemo{key: key, songs: songs, ok: true}
	}
	return m.memo.songs.songs
}

// likedCount says how many songs Liked music has.
func (m *model) likedCount() string {
	gen := m.session.LikesGen()
	if c := &m.memo.liked; !c.ok || c.gen != gen {
		*c = likedMemo{gen: gen, text: locales.AutoPlaylistSongs(m.session.LikedCount()), ok: true}
	}
	return m.memo.liked.text
}
