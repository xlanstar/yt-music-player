package main

import (
	"strings"
	"yt-music-player/locales"

	"github.com/egoist/mygo/i18n"
)

// phrase says a message of string arguments in one allocation. Formatting
// a message allocates about ten times, and rows, the seek bar's value and
// search remake theirs often: a phrase formats it once per language with
// markers for its arguments, then fills them in. UI thread only.
type phrase struct {
	n      int
	format func(args [2]string) string
	lang   string
	isSet  bool
	// parts are the text around the arguments; order is the argument
	// each gap takes, as a translation may swap them.
	parts [3]string
	order [2]int
	// exact is false when the markers did not come back once each, so
	// fill formats every time.
	exact bool
}

// markers stand for the arguments: private-use characters no message has.
var markers = [2]string{"\uE000", "\uE001"}

func phrase1(say func(string) string) *phrase {
	return &phrase{n: 1, format: func(args [2]string) string { return say(args[0]) }}
}

func phrase2(say func(string, string) string) *phrase {
	return &phrase{n: 2, format: func(args [2]string) string { return say(args[0], args[1]) }}
}

func (p *phrase) say1(a string) string    { return p.fill([2]string{a}) }
func (p *phrase) say2(a, b string) string { return p.fill([2]string{a, b}) }

func (p *phrase) fill(args [2]string) string {
	if lang := i18n.Locale(); !p.isSet || lang != p.lang {
		p.learn(lang)
	}
	if !p.exact {
		return p.format(args)
	}
	size := len(p.parts[p.n])
	for i := range p.n {
		size += len(p.parts[i]) + len(args[p.order[i]])
	}
	var b strings.Builder
	b.Grow(size)
	for i := range p.n {
		b.WriteString(p.parts[i])
		b.WriteString(args[p.order[i]])
	}
	b.WriteString(p.parts[p.n])
	return b.String()
}

func (p *phrase) learn(lang string) {
	p.lang, p.isSet, p.exact = lang, true, false
	rest := p.format(markers)
	seen := [2]bool{}
	for i := range p.n {
		at, which := -1, 0
		for m := range p.n {
			if j := strings.Index(rest, markers[m]); j >= 0 && (at < 0 || j < at) {
				at, which = j, m
			}
		}
		if at < 0 || seen[which] {
			return
		}
		seen[which] = true
		p.parts[i], p.order[i] = rest[:at], which
		rest = rest[at+len(markers[which]):]
	}
	if strings.Contains(rest, markers[0]) || strings.Contains(rest, markers[1]) {
		return
	}
	p.parts[p.n], p.exact = rest, true
}

// Phrases the view says often; the rest are said through locales.
var (
	songByArtist = phrase2(locales.SongByArtist)
	seekValue    = phrase2(locales.SeekValue)
	fromAlbum    = phrase1(locales.FromAlbum)
	playlistBy   = phrase1(locales.PlaylistBy)
	searchingFor = phrase1(locales.SearchingFor)
	searchNone   = phrase1(locales.SearchNone)
	quoted       = phrase1(locales.Quoted)
)
