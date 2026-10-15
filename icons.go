package main

import (
	"bytes"
	"embed"

	"github.com/egoist/mygo/ui"
)

// Lucide icons, fetched by scripts/fetch-lucide-icons.sh.
//
//go:embed resources/icons/*.svg
var iconFiles embed.FS

func iconData(name string) []byte {
	data, err := iconFiles.ReadFile("resources/icons/" + name + ".svg")
	if err != nil {
		panic(err)
	}
	return data
}

func mustIcon(name string) *ui.SVG { return ui.MustParseSVG(iconData(name)) }

// mustFilledIcon fills a Lucide outline, keeping its stroke for the
// rounded corners: outlined play buttons read poorly at small sizes, and
// Lucide has no filled set.
func mustFilledIcon(name string) *ui.SVG {
	none, fill := []byte(`fill="none"`), []byte(`fill="currentColor"`)
	data := iconData(name)
	if !bytes.Contains(data, none) {
		panic("icon " + name + ": no fill=\"none\" to fill")
	}
	return ui.MustParseSVG(bytes.Replace(data, none, fill, 1))
}

var (
	icHome         = mustIcon("house")
	icCompass      = mustIcon("compass")
	icLibrary      = mustIcon("library")
	icSearch       = mustIcon("search")
	icPlay         = mustFilledIcon("play")
	icPause        = mustFilledIcon("pause")
	icSkipBack     = mustFilledIcon("skip-back")
	icSkipForward  = mustFilledIcon("skip-forward")
	icShuffle      = mustIcon("shuffle")
	icRepeat       = mustIcon("repeat")
	icRepeatOne    = mustIcon("repeat-1")
	icHeart        = mustIcon("heart")
	icHeartFilled  = mustFilledIcon("heart")
	icVolume       = mustIcon("volume-2")
	icVolumeMuted  = mustIcon("volume-x")
	icQueue        = mustIcon("list-music")
	icLyrics       = mustIcon("message-circle-text")
	icChevronLeft  = mustIcon("chevron-left")
	icChevronRight = mustIcon("chevron-right")
	icPlus         = mustIcon("plus")
	icMore         = mustIcon("ellipsis")
	icPanel        = mustIcon("panel-right")
	icBell         = mustIcon("bell")
	icClock        = mustIcon("clock")
	icDownload     = mustIcon("download")
	icClose        = mustIcon("x")
	icSparkles     = mustIcon("sparkles")
	icTrending     = mustIcon("trending-up")
	icGrid         = mustIcon("layout-grid")
	icDisc         = mustIcon("disc")
	icRadio        = mustIcon("radio")
	icPalette      = mustIcon("palette")
)
