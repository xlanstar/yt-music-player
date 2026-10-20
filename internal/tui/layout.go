package tui

import "image"

// minBody is the least body worth showing; a shorter screen shows only
// the player bar, under the banner.
const minBody = 1

// The parts' spacing, in cells.
const (
	// padX keeps a part's text off the screen's sides.
	padX = 1
	// gapX keeps text off the cover beside it.
	gapX = 1
)

// beside is a part's cover and the text beside it, in the part's cells:
// the cover from its padding, the text after it.
type beside struct {
	// cover is none where empty.
	cover image.Rectangle
	// textX is the text's first cell.
	textX int
}

// layBeside lays out a cover cols×rows from row y and the text beside
// it; no cols is no cover, the text from the padding.
func layBeside(y, cols, rows int) beside {
	if cols <= 0 || rows <= 0 {
		return beside{textX: padX}
	}
	cover := image.Rect(padX, y, padX+cols, y+rows)
	return beside{cover: cover, textX: cover.Max.X + gapX}
}

// layoutIn is what the screen's layout reads: its size, the banner, and
// the rows the tabs and the player bar take.
type layoutIn struct {
	w, h      int
	hasBanner bool
	tabsRows  int
	barRows   int
}

// frameRects are the screen's parts, top to bottom, in cells. They are
// logical: on a screen too short for the bar, the bar keeps its last
// rows and its top is above the screen; the parts not shown have no rows.
type frameRects struct {
	tabs, body, banner, bar image.Rectangle
}

// layout places the tabs, the body, the banner and the bar; the bar ends
// on the screen's last row once the body fits.
func layout(in layoutIn) frameRects {
	bannerRows := 0
	if in.hasBanner {
		bannerRows = 1
	}
	var r frameRects
	y := min(0, in.h-bannerRows-in.barRows)
	if body := in.h - in.tabsRows - in.barRows - bannerRows; body >= minBody {
		r.tabs = band(in.w, 0, in.tabsRows)
		r.body = band(in.w, in.tabsRows, body)
		y = r.body.Max.Y
	}
	r.banner = band(in.w, y, bannerRows)
	r.bar = band(in.w, r.banner.Max.Y, in.barRows)
	return r
}

// band is n rows of w cells from row y. Unlike image.Rect it keeps y as
// the top when n is 0.
func band(w, y, n int) image.Rectangle {
	return image.Rectangle{Min: image.Pt(0, y), Max: image.Pt(w, y+n)}
}

// splitBody splits the body: the page's header on its first headerRows,
// as many as fit, and its message or list in the rows left.
func splitBody(body image.Rectangle, headerRows int) (header, content image.Rectangle) {
	y := min(body.Min.Y+max(headerRows, 0), body.Max.Y)
	header = image.Rectangle{Min: body.Min, Max: image.Pt(body.Max.X, y)}
	content = image.Rectangle{Min: image.Pt(body.Min.X, y), Max: body.Max}
	return header, content
}
