package tui

import (
	"strconv"
	"strings"
	"yt-music-player/locales"
)

// segment is a cell of a row: text in its style, fitted to width cells,
// aligned right if isRight.
type segment struct {
	text    string
	width   int
	isRight bool
	style   paint
}

// The columns' widths and the gap between them, in cells; cells measures
// the glyphs' columns.
const (
	colGap   = 2
	durWidth = 6 // " 12:34"
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

// draw draws s from cell x of row y of g, and returns the cell after it.
func (s *segment) draw(g region, x, y int, c *cells) int {
	text, w := c.fitted(s.text, s.width)
	at := x
	if s.isRight {
		at += s.width - w
	}
	g.putText(at, y, text, w, s.style)
	return x + s.width
}

// write writes s's text, plain and padded to its width, to b.
func (s *segment) write(b *strings.Builder, c *cells) {
	text, w := c.fitted(s.text, s.width)
	if s.isRight {
		writeSpaces(b, s.width-w)
	}
	b.WriteString(text)
	if !s.isRight {
		writeSpaces(b, s.width-w)
	}
}

// drawRow draws it on row y of g, a list's, from the screen's left edge
// to its right. The selected row is one reversed span across, behind a
// marker that shows without colors too.
func (m *model) drawRow(g region, y int, it item, isSelected bool) {
	c := &m.cells
	w, _ := g.size()
	if it.kind == itemHeading {
		g.fit(padX, y, it.text, max(w-2*padX, 0), styleHeading)
		return
	}
	// The padding and the cursor's column before, the padding after.
	inner := max(w-padX-c.cursor-padX, 0)
	blank := segment{width: c.mark}
	var segs [8]segment
	var parts []segment
	switch it.kind {
	case itemSong:
		parts = m.songSegments(segs[:0], &it, inner)
	case itemPlayAll:
		parts = append(segs[:0], segment{text: iconPlayAll, width: c.mark, style: styleAccent},
			segment{text: locales.PlayAll(), width: max(inner-c.mark, 1), style: styleAccent})
	case itemLiked:
		liked := segment{text: iconLiked, width: c.mark, style: styleAccent}
		parts = columns(append(segs[:0], liked), locales.LikedMusic(), it.text, inner-c.mark)
	case itemAlbum:
		detail := it.album.Artist
		if it.album.Year > 0 {
			detail += separator + strconv.Itoa(it.album.Year)
		}
		parts = columns(append(segs[:0], blank), it.album.Title, detail, inner-c.mark)
	case itemPlaylist:
		parts = columns(append(segs[:0], blank), it.playlist.Name, it.playlist.Owner, inner-c.mark)
	}
	if !isSelected {
		x := padX + c.cursor
		for i := range parts {
			x = parts[i].draw(g, x, y, c)
		}
		return
	}
	var b strings.Builder
	b.Grow(4 * w)
	writeSpaces(&b, padX)
	b.WriteString(cursorMark)
	b.WriteByte(' ')
	used := padX + c.cursor
	for i := range parts {
		parts[i].write(&b, c)
		used += parts[i].width
	}
	writeSpaces(&b, w-used)
	g.putText(0, y, b.String(), max(used, w), styleSelected)
}

// songSegments lays a song out in w cells: a mark if current, its title,
// artist and album as room allows, its length and a heart if liked.
func (m *model) songSegments(segs []segment, it *item, w int) []segment {
	s, c := &it.song, &m.cells
	mark, title := "", paint{}
	if it.isCurrent {
		mark, title = iconCurrent, styleAccent
	}
	rest := w - c.mark - durWidth - c.heart
	if rest < 1 {
		return append(segs, segment{text: s.Title, width: max(w, 1), style: title})
	}
	segs = append(segs, segment{text: mark, width: c.mark, style: styleAccent})
	switch {
	case rest >= albumFrom:
		cols := rest - 2*colGap
		tw, aw := cols*45/100, cols*30/100
		segs = append(segs,
			segment{text: s.Title, width: tw, style: title}, segment{width: colGap},
			segment{text: s.Artist, width: aw, style: styleMuted}, segment{width: colGap},
			segment{text: s.Album, width: cols - tw - aw, style: styleMuted})
	case rest >= artistFrom:
		cols := rest - colGap
		tw := cols * 60 / 100
		segs = append(segs,
			segment{text: s.Title, width: tw, style: title}, segment{width: colGap},
			segment{text: s.Artist, width: cols - tw, style: styleMuted})
	default:
		segs = append(segs, segment{text: s.Title, width: rest, style: title})
	}
	dur := ""
	if s.Seconds > 0 {
		dur = Clock(float64(s.Seconds))
	}
	heart := ""
	if m.session.Liked(s.ID) {
		heart = iconLiked
	}
	return append(segs,
		segment{text: dur, width: durWidth, isRight: true, style: styleMuted},
		segment{text: heart, width: c.heart, isRight: true, style: styleAccent})
}

// columns lays out a label and its detail in w cells, as a song's title
// and artist.
func columns(segs []segment, label, detail string, w int) []segment {
	w = max(w, 1)
	if w < artistFrom || detail == "" {
		return append(segs, segment{text: label, width: w})
	}
	lw := (w - colGap) * 55 / 100
	return append(segs,
		segment{text: label, width: lw}, segment{width: colGap},
		segment{text: detail, width: w - lw - colGap, style: styleMuted})
}
