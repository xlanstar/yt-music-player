package tui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"yt-music-player/internal/artwork"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/termimage"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
)

const coverURL = "https://lh3.googleusercontent.com/cover=w120-h120"

// coverHost serves a red PNG for every URL, counting the requests.
type coverHost struct {
	png   []byte
	calls atomic.Int32
}

func newCoverHost(t testing.TB) *coverHost {
	t.Helper()
	m := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for i := 0; i < len(m.Pix); i += 4 {
		copy(m.Pix[i:], []byte{220, 30, 40, 255})
	}
	var b bytes.Buffer
	if err := png.Encode(&b, m); err != nil {
		t.Fatal(err)
	}
	return &coverHost{png: b.Bytes()}
}

func (c *coverHost) RoundTrip(*http.Request) (*http.Response, error) {
	c.calls.Add(1)
	return &http.Response{
		StatusCode:    http.StatusOK,
		Body:          io.NopCloser(bytes.NewReader(c.png)),
		ContentLength: int64(len(c.png)),
		Header:        http.Header{},
	}, nil
}

// withCovers shows covers as YTM_IMAGES=p says, of 10×20-pixel cells, the
// current song's from host.
func (h *harness) withCovers(p termimage.Protocol, host *coverHost) {
	h.t.Helper()
	posts := h.m.posts
	h.m.gfx = gfx{
		env:     termimage.Env{Override: p.String()},
		replies: termimage.Replies{CellW: 10, CellH: 20},
		post:    posts.post,
		newArt: func() *artwork.Loader {
			return artwork.New(artwork.Options{
				Post: posts.post, Client: &http.Client{Transport: host}, MemoryBytes: artMemory, Workers: 1,
			})
		},
	}
	if cmd := h.m.gfx.start(); cmd != nil {
		h.t.Fatal("an override probes nothing")
	}
	h.t.Cleanup(func() { h.m.gfx.close() })
	song := h.mirror.songs[h.mirror.state.Current]
	song.Art = coverURL
	h.mirror.songs[song.ID] = song
}

// covered settles until the page and the covers shown loaded and encoded,
// and returns what went to the terminal meanwhile.
func (h *harness) covered() []string {
	h.t.Helper()
	raws := h.run(h.send(postedMsg{}))
	deadline := time.After(5 * time.Second)
	for h.m.gfx.isBusy() || h.m.loader.IsBusy() {
		select {
		case <-h.m.posts.wake:
			raws = append(raws, h.run(h.send(postedMsg{}))...)
		case <-deadline:
			h.t.Fatalf("never covered:\n%s", h.screen())
		}
	}
	return raws
}

// run runs cmd as Bubble Tea would, sending overlayMsgs back, and
// returns the sequences written; nothing else may wait long.
func (h *harness) run(cmd tea.Cmd) []string {
	if cmd == nil {
		return nil
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		var raws []string
		for _, c := range msg {
			raws = append(raws, h.run(c)...)
		}
		return raws
	case tea.RawMsg:
		return []string{fmt.Sprint(msg.Msg)}
	case overlayMsg:
		return h.run(h.send(msg))
	}
	return nil
}

func TestCoverBlocks(t *testing.T) {
	host := newCoverHost(t)
	h := placeholderHarness(t)
	h.withCovers(termimage.Blocks, host)
	if raws := h.covered(); len(raws) != 0 {
		t.Errorf("blocks wrote %q, want only text", raws)
	}
	h.golden("queue-cover-blocks")
	if !strings.Contains(h.m.content, "\x1b[38;2;220;30;40;48;2;220;30;40m▀") {
		t.Error("the cover is not the host's red")
	}
	// The progress starts after the cover.
	y, start, _ := h.progressBar()
	if line := strings.Split(h.screen(), "\n")[y]; !strings.HasPrefix(line, " ▀▀▀▀ 0:41 ") || start != 11 {
		t.Errorf("progress at %d: %q", start, line)
	}
	h.resize(30, 24)
	if strings.Contains(h.screen(), "▀") {
		t.Error("a narrow terminal shows the cover")
	}
	if got := host.calls.Load(); got != 1 {
		t.Errorf("fetched the cover %d times, want 1", got)
	}
}

func TestCoverKitty(t *testing.T) {
	h := placeholderHarness(t)
	h.withCovers(termimage.Kitty, newCoverHost(t))
	raws := h.covered()
	if len(raws) != 1 || !strings.HasPrefix(raws[0], "\x1b_Ga=T,U=1,q=2,f=24,s=40,v=40,i=176,c=4,r=2,") {
		t.Fatalf("uploads = %.60q, want one of image 176", raws)
	}
	if want := termimage.KittyCells(176, 4, 2)[0]; !strings.Contains(h.m.content, " "+want+" ") {
		t.Errorf("the bar lacks the placeholders %q", want)
	}
	// Shown again, the image is the terminal's.
	if raws := h.run(h.press("tab")); len(raws) != 0 {
		t.Errorf("a render sent %q again", raws)
	}
	if got := h.m.gfx.close(); got != termimage.KittyDelete(176, false) {
		t.Errorf("close = %q, want image 176 deleted", got)
	}
}

func TestCoverSixel(t *testing.T) {
	h := placeholderHarness(t)
	h.withCovers(termimage.Sixel, newCoverHost(t))
	raws := h.covered()
	// The song's line, 22, from cell 2: after the frame that blanked them.
	if len(raws) != 1 || !strings.HasPrefix(raws[0], "\x1b7\x1b[22;2H\x1bP0;1;0q\"1;1;40;40#") {
		t.Fatalf("overlays = %.40q, want the cover drawn once", raws)
	}
	if !strings.Contains(h.screen(), " \u2800\u2800\u2800\u2800 ") {
		t.Error("the cover's cells are not left blank")
	}
	if raws := h.run(h.press("tab")); len(raws) != 0 {
		t.Errorf("a render that leaves the cover drew %q", raws)
	}
	// A resize repaints every cell, the cover's too.
	raws = h.run(h.send(tea.WindowSizeMsg{Width: 90, Height: 24}))
	if len(raws) != 2 || raws[0] != "\x1b[16t" || !strings.HasPrefix(raws[1], "\x1b7\x1b[22;2H") {
		t.Errorf("after a resize, wrote %.40q", raws)
	}
	// Without the cover, its cells are painted over: nothing to draw.
	h.mirror.setQueue(nil)
	if raws := h.run(h.send(postedMsg{})); len(raws) != 0 {
		t.Errorf("without a cover, drew %q", raws)
	}
	if h.m.gfx.drawn != ([numSlots]placement{}) {
		t.Errorf("drawn = %+v, want none", h.m.gfx.drawn)
	}
}

func TestCoverITerm2(t *testing.T) {
	h := placeholderHarness(t)
	h.withCovers(termimage.ITerm2, newCoverHost(t))
	raws := h.covered()
	if len(raws) != 1 || !strings.HasPrefix(raws[0], "\x1b7\x1b[22;2H\x1b]1337;File=inline=1;") {
		t.Fatalf("overlays = %.60q, want the cover drawn once", raws)
	}
}

// albumArt is the placeholder with covers fetched from coverURL.
type albumArt struct{ *catalog.PlaceholderSource }

func (s albumArt) Album(ctx context.Context, id string) (catalog.Album, []catalog.Song, error) {
	a, songs, err := s.PlaceholderSource.Album(ctx, id)
	a.Art = coverURL
	return a, songs, err
}

func TestCoverHeader(t *testing.T) {
	h := newHarness(t, albumArt{catalog.NewPlaceholderSource(0)})
	h.withCovers(termimage.Kitty, newCoverHost(t))
	h.m.open(opened{kind: itemAlbum, id: catalog.Placeholder().Albums[0].ID})
	// Not loaded(), which would drop an upload made meanwhile.
	raws := h.covered()
	// One image: the album's is the song's.
	if len(raws) != 1 {
		t.Fatalf("uploads = %d, want 1", len(raws))
	}
	lines := strings.Split(h.m.content, "\n")
	cells := termimage.KittyCells(176, 4, 2)
	if !strings.HasPrefix(lines[2], " "+cells[0]+" ") || !strings.HasPrefix(lines[3], " "+cells[1]+" ") {
		t.Errorf("the header lacks the cover:\n%q\n%q", lines[2], lines[3])
	}
	if h.m.gfx.placed[slotHeader].y != 2 || h.m.gfx.placed[slotBar].y != 21 {
		t.Errorf("placed = %+v", h.m.gfx.placed)
	}
}

// TestCoverOverlayReturns draws a header's image again after the help
// hid and showed it before the overlay's frame came.
func TestCoverOverlayReturns(t *testing.T) {
	h := newHarness(t, albumArt{catalog.NewPlaceholderSource(0)})
	h.withCovers(termimage.Sixel, newCoverHost(t))
	h.m.open(opened{kind: itemAlbum, id: catalog.Placeholder().Albums[0].ID})
	if raws := h.covered(); len(raws) != 1 || !strings.Contains(raws[0], "\x1b[3;2H") {
		t.Fatalf("overlays = %.40q, want the header's", raws)
	}
	h.press("?")
	raws := h.run(h.press("?"))
	if len(raws) != 1 || !strings.HasPrefix(raws[0], "\x1b7\x1b[3;2H\x1bP") || strings.Contains(raws[0], "\x1b[22;2H") {
		t.Errorf("back from the help, drew %.40q, want only the header's cover", raws)
	}
}

func TestCoverOff(t *testing.T) {
	host := newCoverHost(t)
	h := placeholderHarness(t)
	h.withCovers(termimage.None, host)
	before := h.screen()
	h.covered()
	if h.m.gfx.art != nil || host.calls.Load() != 0 {
		t.Error("covers off made a loader or fetched")
	}
	if h.screen() != before {
		t.Error("covers off changed the screen")
	}
}

func TestCoverDetect(t *testing.T) {
	made := 0
	g := gfx{env: termimage.Env{Term: "foot"}, newArt: func() *artwork.Loader {
		made++
		return nil
	}}
	if g.start() == nil {
		t.Fatal("start asked nothing")
	}
	if g.reply(uv.CellSizeEvent{Width: 9, Height: 18}) || g.reply(tea.TerminalVersionMsg{Name: "foot(1.20)"}) {
		t.Error("covers changed before the device attributes")
	}
	if !g.reply(uv.PrimaryDeviceAttributesEvent{62, 4}) || g.caps.Protocol != termimage.Sixel || g.cols != 4 {
		t.Errorf("caps = %+v, cols %d; want Sixel of 4 columns", g.caps, g.cols)
	}
	if made != 1 {
		t.Errorf("made %d loaders, want 1", made)
	}
	if g.reply(probeTimeoutMsg{}) {
		t.Error("the probe's wait changed what detected")
	}
	// A larger font makes covers narrower.
	if !g.reply(uv.CellSizeEvent{Width: 20, Height: 20}) || g.cols != 2 {
		t.Errorf("cols = %d after square cells, want 2", g.cols)
	}

	g = gfx{env: termimage.Env{Term: "xterm-256color"}}
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
}

// TestCoverEvicts reuses the cover placed longest ago.
func TestCoverEvicts(t *testing.T) {
	g := gfx{caps: termimage.Caps{Protocol: termimage.Blocks}}
	g.fit()
	for i := range numCovers + 2 {
		g.beginFrame()
		g.place(slotBar, 1, 0, catalog.Art(fmt.Sprintf("https://x/%d", i)))
	}
	seen := map[string]bool{}
	for _, c := range g.covers {
		seen[c.key.url] = true
	}
	if seen["https://x/0"] || seen["https://x/1"] || !seen["https://x/9"] {
		t.Errorf("kept %v, want the last %d", seen, numCovers)
	}
}

func TestIsURL(t *testing.T) {
	if !isURL(coverURL) || isURL("generated:neon-tide") {
		t.Error("isURL tells a generated cover from a fetched one wrong")
	}
}
