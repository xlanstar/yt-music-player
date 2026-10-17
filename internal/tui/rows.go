package tui

import (
	"strconv"
	"strings"
	"yt-music-player/locales"
)

// segment is a cell of a row: text in its style, fitted to width cells
// (aligned right if isRight), or as it is when width is 0.
type segment struct {
	text    string
	width   int
	isRight bool
	style   paint
}

// The columns' widths and the gap between them, in cells.
const (
	gap        = "  "
	durWidth   = 6 // " 12:34"
	heartWidth = 2
	markWidth  = 2 // "♪ "
	// Song rows show the artist from this width up, the album from
	// albumFrom.
	artistFrom = 24
	albumFrom  = 50
)

// spaces pads cells without allocating.
var spaces = strings.Repeat(" ", 256)

// writeSpaces writes n spaces to b.
func writeSpaces(b *strings.Builder, n int) {
	for n > 0 {
		k := min(n, len(spaces))
		b.WriteString(spaces[:k])
		n -= k
	}
}

// write writes s to b, in style unless plain.
func (s *segment) write(b *strings.Builder, isPlain bool) {
	text, fill := s.text, 0
	if s.width > 0 {
		text = fit(text, s.width)
		fill = s.width - width(text)
	}
	if s.isRight {
		writeSpaces(b, fill)
	}
	if isPlain {
		b.WriteString(text)
	} else {
		s.style.write(b, text)
	}
	if !s.isRight {
		writeSpaces(b, fill)
	}
}

// row draws it on a line of w cells. The selected row is reversed, behind
// a marker that shows without colors too.
func (m *model) row(it item, w int, isSelected bool) string {
	if it.kind == itemHeading {
		return " " + styleHeading.Render(fit(it.text, max(w-2, 0)))
	}
	// " ▸ " before, a space after.
	inner := max(w-4, 0)
	var segs [8]segment
	var parts []segment
	switch it.kind {
	case itemSong:
		parts = m.songSegments(segs[:0], &it, inner)
	case itemPlayAll:
		parts = append(segs[:0], segment{text: "▶ ", style: styleAccent},
			segment{text: locales.PlayAll(), width: max(inner-markWidth, 1), style: styleAccent})
	case itemLiked:
		parts = columns(append(segs[:0], segment{text: "♥ ", style: styleAccent}), locales.LikedMusic(), it.text, inner-markWidth)
	case itemAlbum:
		detail := it.album.Artist
		if it.album.Year > 0 {
			detail += " · " + strconv.Itoa(it.album.Year)
		}
		parts = columns(append(segs[:0], segment{text: "  "}), it.album.Title, detail, inner-markWidth)
	case itemPlaylist:
		parts = columns(append(segs[:0], segment{text: "  "}), it.playlist.Name, it.playlist.Owner, inner-markWidth)
	}
	var b strings.Builder
	b.Grow(4 * w)
	if !isSelected {
		b.WriteString("   ")
		for i := range parts {
			parts[i].write(&b, false)
		}
		return b.String()
	}
	b.WriteString(styleSelected.pre)
	b.WriteString(" ▸ ")
	used := 3
	for i := range parts {
		start := b.Len()
		parts[i].write(&b, true)
		used += width(b.String()[start:])
	}
	writeSpaces(&b, w-used)
	b.WriteString(styleSelected.post)
	return b.String()
}

// songSegments lays a song out in w cells: a mark if current, its title,
// artist and album as room allows, its length and a heart if liked.
func (m *model) songSegments(segs []segment, it *item, w int) []segment {
	s := &it.song
	mark, title := "  ", paint{}
	if it.isCurrent {
		mark, title = "♪ ", styleAccent
	}
	rest := w - markWidth - durWidth - heartWidth
	if rest < 1 {
		return append(segs, segment{text: s.Title, width: max(w, 1), style: title})
	}
	segs = append(segs, segment{text: mark, style: styleAccent})
	switch {
	case rest >= albumFrom:
		cols := rest - 2*len(gap)
		tw, aw := cols*45/100, cols*30/100
		segs = append(segs,
			segment{text: s.Title, width: tw, style: title}, segment{text: gap},
			segment{text: s.Artist, width: aw, style: styleMuted}, segment{text: gap},
			segment{text: s.Album, width: cols - tw - aw, style: styleMuted})
	case rest >= artistFrom:
		cols := rest - len(gap)
		tw := cols * 60 / 100
		segs = append(segs,
			segment{text: s.Title, width: tw, style: title}, segment{text: gap},
			segment{text: s.Artist, width: cols - tw, style: styleMuted})
	default:
		segs = append(segs, segment{text: s.Title, width: rest, style: title})
	}
	dur := ""
	if s.Seconds > 0 {
		dur = clock(float64(s.Seconds))
	}
	heart := "  "
	if m.session.Liked(s.ID) {
		heart = " ♥"
	}
	return append(segs,
		segment{text: dur, width: durWidth, isRight: true, style: styleMuted},
		segment{text: heart, style: styleAccent})
}

// columns lays out a label and its detail in w cells, as a song's title
// and artist.
func columns(segs []segment, label, detail string, w int) []segment {
	w = max(w, 1)
	if w < artistFrom || detail == "" {
		return append(segs, segment{text: label, width: w})
	}
	lw := (w - len(gap)) * 55 / 100
	return append(segs,
		segment{text: label, width: lw}, segment{text: gap},
		segment{text: detail, width: w - lw - len(gap), style: styleMuted})
}
