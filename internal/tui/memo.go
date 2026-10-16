package tui

import (
	"context"
	"errors"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/locales"
)

// memo holds what renders derive, made again only when its inputs change.
type memo struct {
	songs  songsMemo
	liked  likedMemo
	volume volumeMemo
}

type songsKey struct {
	o                opened
	catGen, likesGen uint64
}

type songsMemo struct {
	key   songsKey
	songs []catalog.Song
	ok    bool
}

type likedMemo struct {
	gen  uint64
	text string
	ok   bool
}

type volumeMemo struct {
	percent int
	muted   bool
	text    string
	ok      bool
}

// words are the messages the screen says without arguments, said once:
// ytm picks its language at start.
type words struct {
	queue, search, library string
	notSignedIn            string

	queueEmpty, queueEmptyHint    string
	searchTitle, searchHint       string
	searchPlaceholder, searching  string
	searchNoneHint                string
	songs, albums, playlists      string
	likedMusic, playAll           string
	loading, retry                string
	notFoundTitle, notFoundDetail string
	nothingHereYet, likedEmpty    string
	libraryFailed, likesFailed    string
	albumFailed, playlistFailed   string

	nothingPlaying              string
	shuffleOn                   string
	repeatAll, rOne             string
	restart                     string
	connecting, reconnecting    string
	unresponsive, unavailable   string
	whyDisconnected, whyTimeout string
	whySignedOut, whyCanceled   string
}

func newWords() words {
	return words{
		queue:             locales.Queue(),
		search:            locales.NavSearch(),
		library:           locales.NavLibrary(),
		notSignedIn:       locales.CliSignedOut(),
		queueEmpty:        locales.TuiQueueEmpty(),
		queueEmptyHint:    locales.TuiQueueEmptyHint(),
		searchTitle:       locales.NavSearch(),
		searchHint:        locales.TuiSearchHint(),
		searchPlaceholder: locales.SearchPlaceholder(),
		searching:         locales.Searching(),
		searchNoneHint:    locales.SearchNoneHint(),
		songs:             locales.Songs(),
		albums:            locales.Albums(),
		playlists:         locales.SidebarPlaylists(),
		likedMusic:        locales.LikedMusic(),
		playAll:           locales.PlayAll(),
		loading:           locales.Loading(),
		retry:             locales.Retry(),
		notFoundTitle:     locales.NotFoundTitle(),
		notFoundDetail:    locales.NotFoundDetail(),
		nothingHereYet:    locales.NothingHereYet(),
		likedEmpty:        locales.LikedEmpty(),
		libraryFailed:     locales.LibraryFailed(),
		likesFailed:       locales.LikesFailed(),
		albumFailed:       locales.AlbumFailed(),
		playlistFailed:    locales.PlaylistFailed(),
		nothingPlaying:    locales.TuiNothingPlaying(),
		shuffleOn:         locales.CliShuffleOn(),
		repeatAll:         locales.RepeatAll(),
		rOne:              locales.RepeatOne(),
		restart:           locales.TuiRestart(),
		connecting:        locales.TuiConnecting(),
		reconnecting:      locales.TuiReconnecting(),
		unresponsive:      locales.TuiUnresponsive(),
		unavailable:       locales.TuiUnavailable(),
		whyDisconnected:   locales.CliWhyDisconnected(),
		whyTimeout:        locales.CliWhyTimeout(),
		whySignedOut:      locales.CliWhySignedOut(),
		whyCanceled:       locales.CliWhyCanceled(),
	}
}

// why says err in the user's words. Only an unforeseen error is said as
// it is.
func (m *model) why(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, api.ErrDisconnected):
		return m.words.whyDisconnected
	case errors.Is(err, context.DeadlineExceeded):
		return m.words.whyTimeout
	case errors.Is(err, context.Canceled):
		return m.words.whyCanceled
	case errors.Is(err, catalog.ErrSignedOut):
		return m.words.whySignedOut
	case errors.Is(err, api.ErrInternal):
		return locales.CliWhyDaemonFailed(m.daemonLog)
	}
	return err.Error()
}
