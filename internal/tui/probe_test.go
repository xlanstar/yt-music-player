package tui

import (
	"image"
	"testing"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/termimage"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
)

func TestCoverDetect(t *testing.T) {
	g := probe{env: termimage.Env{Term: "foot"}}
	if g.start() == nil {
		t.Fatal("start asked nothing")
	}
	if g.reply(uv.CellSizeEvent{Width: 9, Height: 18}) || g.reply(tea.TerminalVersionMsg{Name: "foot(1.20)"}) {
		t.Error("covers changed before the device attributes")
	}
	if !g.reply(uv.PrimaryDeviceAttributesEvent{62, 4}) || g.caps.Protocol != termimage.Sixel || g.coverCols(2, 80) != 4 {
		t.Errorf("caps = %+v, cols %d; want Sixel of 4 columns", g.caps, g.coverCols(2, 80))
	}
	if g.reply(probeTimeoutMsg{}) {
		t.Error("the probe's wait changed what detected")
	}
	// A larger font makes covers narrower.
	if !g.reply(uv.CellSizeEvent{Width: 20, Height: 20}) || g.coverCols(2, 80) != 2 {
		t.Errorf("cols = %d after square cells, want 2", g.coverCols(2, 80))
	}

	// The Linux console shows no images, and would print the probe.
	g = probe{env: termimage.Env{Term: "linux"}}
	if cmd := g.start(); cmd != nil {
		t.Error("on the Linux console, start wrote the probe")
	}
	if !g.isReady || g.caps.Protocol != termimage.None {
		t.Errorf("on the Linux console, ready %t, caps = %+v", g.isReady, g.caps)
	}

	g = probe{env: termimage.Env{Term: "xterm-256color"}}
	g.reply(tea.ColorProfileMsg{Profile: colorprofile.TrueColor})
	if !g.reply(probeTimeoutMsg{}) || g.caps.Protocol != termimage.Blocks {
		t.Errorf("unanswered, caps = %+v, want Blocks", g.caps)
	}
	if !g.reply(tea.ColorProfileMsg{Profile: colorprofile.ANSI}) || g.caps.Protocol != termimage.None {
		t.Errorf("sixteen colors, caps = %+v, want None", g.caps)
	}
	if g.reply(uv.KittyGraphicsEvent{}) || g.replies.Kitty {
		t.Error("another image's reply counted as the probe's")
	}
	if g.reply(tickMsg{}) {
		t.Error("another message changed the covers")
	}
}

// TestCoverRequest sizes a cover from its rows, square in the cell's
// pixels, and omits it where it would not fit beside the text.
func TestCoverRequest(t *testing.T) {
	g := probe{env: termimage.Env{Override: "sixel"}, replies: termimage.Replies{CellW: 10, CellH: 20}}
	g.detect()
	for _, tc := range []struct {
		rows, w int
		art     catalog.Art
		want    image.Rectangle
	}{
		{2, 80, coverURL, image.Rect(1, 5, 5, 7)},
		{3, 80, coverURL, image.Rect(1, 5, 7, 8)},
		{1, 80, coverURL, image.Rect(1, 5, 3, 6)},
		// 6 cells and 40 beside them.
		{3, 46, coverURL, image.Rect(1, 5, 7, 8)},
		{3, 45, coverURL, image.Rectangle{}},
		{0, 80, coverURL, image.Rectangle{}},
		{2, 80, "", image.Rectangle{}},
		{2, 80, "icon:liked", image.Rectangle{}},
	} {
		r, ok := g.request(slotBar, 5, tc.rows, tc.w, tc.art)
		if r.rect != tc.want || ok != (tc.want != image.Rectangle{}) {
			t.Errorf("%d rows of %q, %d wide: %v, %t; want %v", tc.rows, tc.art, tc.w, r.rect, ok, tc.want)
		}
	}
	g = probe{env: termimage.Env{Override: "off"}}
	g.detect()
	if _, ok := g.request(slotBar, 5, 2, 200, coverURL); ok {
		t.Error("without a protocol, a cover shows")
	}
}
