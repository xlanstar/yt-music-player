package tui

import (
	"yt-music-player/locales"
)

// memo holds what renders derive, made again only when its inputs change.
type memo struct {
	volume volumeMemo
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
	queue       string
	notSignedIn string

	queueEmpty, queueEmptyHint string
	retry                      string

	nothingPlaying            string
	shuffleOn                 string
	repeatAll, rOne           string
	restart                   string
	connecting, reconnecting  string
	unresponsive, unavailable string
}

func newWords() words {
	return words{
		queue:          locales.Queue(),
		notSignedIn:    locales.CliSignedOut(),
		queueEmpty:     locales.TuiQueueEmpty(),
		queueEmptyHint: locales.TuiQueueEmptyHint(),
		retry:          locales.Retry(),
		nothingPlaying: locales.TuiNothingPlaying(),
		shuffleOn:      locales.CliShuffleOn(),
		repeatAll:      locales.RepeatAll(),
		rOne:           locales.RepeatOne(),
		restart:        locales.TuiRestart(),
		connecting:     locales.TuiConnecting(),
		reconnecting:   locales.TuiReconnecting(),
		unresponsive:   locales.TuiUnresponsive(),
		unavailable:    locales.TuiUnavailable(),
	}
}
