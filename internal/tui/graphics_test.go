package tui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"slices"
	"strconv"
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
	"github.com/charmbracelet/x/ansi"
)

const coverURL = "https://lh3.googleusercontent.com/cover=w120-h120"

// coverHost serves a red PNG of 128 pixels for every URL, counting the
// requests.
type coverHost struct {
	png   []byte
	calls atomic.Int32
}

func newCoverHost(t testing.TB) *coverHost {
	t.Helper()
	m := image.NewRGBA(image.Rect(0, 0, 128, 128))
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
		env:       termimage.Env{Override: p.String()},
		replies:   termimage.Replies{CellW: 10, CellH: 20},
		kittyBase: 176,
		post:      posts.post,
		newArt: func() *artwork.Loader {
			return artwork.New(artwork.Options{
				Post: posts.post, Client: &http.Client{Transport: host}, MemoryBytes: artMemory, Workers: 1,
			})
		},
	}
	want := []string{termimage.CellSizeQuery}
	if p == termimage.None {
		want = nil
	}
	if raws := h.run(h.m.gfx.start()); !slices.Equal(raws, want) {
		h.t.Fatalf("an override asked %q, want only the cell's pixels", raws)
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

// run runs cmd as Bubble Tea would and returns the sequences written;
// nothing else may wait long.
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
	}
	return nil
}

// overlays are what termOut draws after a frame that writes the whole
// screen: the overlays of the marks the screen holds, from the table the
// model last published.
func (h *harness) overlays() string {
	h.t.Helper()
	g := &h.m.gfx
	if g.table != nil && g.table.Gen() != g.published {
		h.t.Fatalf("table %d not published, %d is", g.table.Gen(), g.published)
	}
	frame := []byte(h.m.content)
	text := termimage.Splice(nil, frame, nil, h.m.width, h.m.height)
	return string(termimage.Splice(nil, frame, g.table, h.m.width, h.m.height)[len(text):])
}

// placeholders are the lines of Kitty's placeholders that show image id
// over a cover's 4×2 cells.
func placeholders(t testing.TB, id uint8) []string {
	t.Helper()
	m := image.NewRGBA(image.Rect(0, 0, 1, 1))
	e, err := termimage.Encode(m, termimage.Caps{Protocol: termimage.Kitty}, 4, coverRows, id, false)
	if err != nil {
		t.Fatal(err)
	}
	return e.Lines
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
	h.m.gfx.kittyBase = 40
	raws := h.covered()
	if len(raws) != 1 || !strings.HasPrefix(raws[0], "\x1b_Ga=T,U=1,q=2,f=24,s=40,v=40,i=40,c=4,r=2,") {
		t.Fatalf("uploads = %.60q, want one of image 40", raws)
	}
	if want := placeholders(t, 40)[0]; !strings.Contains(h.m.content, " "+want+" ") {
		t.Errorf("the bar lacks the placeholders %q", want)
	}
	// Shown again, the image is the terminal's.
	if raws := h.run(h.press("tab")); len(raws) != 0 {
		t.Errorf("a render sent %q again", raws)
	}
	// Another cover takes the slot: image 40 stays the terminal's.
	h.m.gfx.covers[0] = cover{}
	if got := h.m.gfx.close(); !strings.HasPrefix(got, termimage.KittyDelete(40, false)) ||
		!strings.HasSuffix(got, termimage.KittyDelete(40+numCovers-1, false)) {
		t.Errorf("close = %q, want images 40 to %d deleted", got, 40+numCovers-1)
	}
}

// TestKittyBase picks each process's ids among the 256 colors' 1 to 255.
func TestKittyBase(t *testing.T) {
	seen := map[uint8]bool{}
	for range 1000 {
		b := newKittyBase()
		if b == 0 || int(b)+numCovers-1 > 255 {
			t.Fatalf("base %d: ids %d to %d, want 1 to 255", b, b, int(b)+numCovers-1)
		}
		seen[b] = true
	}
	if len(seen) < 2 {
		t.Errorf("every process would pick %v", seen)
	}
}

// TestCoverOverlays draws Sixel's and iTerm2's images over blank cells,
// after the frames that write their marks.
func TestCoverOverlays(t *testing.T) {
	for p, seq := range map[termimage.Protocol]string{
		termimage.Sixel:  "\x1bP0;1;0q\"1;1;40;36#",
		termimage.ITerm2: "\x1b]1337;File=inline=1;",
	} {
		t.Run(p.String(), func(t *testing.T) {
			h := placeholderHarness(t)
			h.withCovers(p, newCoverHost(t))
			if raws := h.covered(); len(raws) != 0 {
				t.Errorf("wrote %.40q besides the screen", raws)
			}
			// The song's line, 22, from cell 2.
			if got := h.overlays(); !strings.HasPrefix(got, "\x1b7\x1b[22;2H"+seq) || strings.Count(got, "\x1b7") != 1 {
				t.Fatalf("overlays = %.60q, want the cover's", got)
			}
			if !strings.Contains(h.screen(), " \u2800\u2800\u2800\u2800 ") {
				t.Error("the cover's cells are not left blank")
			}
			// The same overlays keep their generation, and the lines their
			// bytes: the renderer writes them only when it must.
			gen, content := h.m.gfx.published, h.m.content
			h.press("tab")
			h.press("shift+tab")
			if raws := h.run(h.send(tea.WindowSizeMsg{Width: 90, Height: 24})); len(raws) != 1 || raws[0] != termimage.CellSizeQuery {
				t.Errorf("a resize wrote %.40q, want the cell's size asked", raws)
			}
			h.resize(80, 24)
			if h.m.gfx.published != gen || h.m.content != content {
				t.Errorf("generation %d after %d, the screen the same %t", h.m.gfx.published, gen, h.m.content == content)
			}
			// Without the cover, its cells are painted over: nothing to draw.
			h.mirror.setQueue(nil)
			h.send(postedMsg{})
			if got := h.overlays(); got != "" || h.m.gfx.published != gen+1 || strings.Contains(h.m.content, "\x1b^") {
				t.Errorf("without a cover, overlays %q, generation %d after %d", got, h.m.gfx.published, gen)
			}
		})
	}
}

// TestCoverLarger encodes a cover again once its size loaded: meanwhile
// it showed the one loaded for smaller cells.
func TestCoverLarger(t *testing.T) {
	h := placeholderHarness(t)
	h.withCovers(termimage.Kitty, newCoverHost(t))
	h.covered()
	raws := h.run(h.send(uv.CellSizeEvent{Width: 20, Height: 40}))
	raws = append(raws, h.covered()...)
	uploads := 0
	for _, raw := range raws {
		if strings.Contains(raw, ",s=80,v=80,") {
			uploads++
		}
	}
	if c := h.m.gfx.ready(h.m.gfx.placed[slotBar].key); uploads != 2 || c == nil || c.srcPx != 80 {
		t.Errorf("%d uploads of 80 pixels, want the 64's then the 80's", uploads)
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
	cells := placeholders(t, 176)
	if !strings.HasPrefix(lines[2], " "+cells[0]+" ") || !strings.HasPrefix(lines[3], " "+cells[1]+" ") {
		t.Errorf("the header lacks the cover:\n%q\n%q", lines[2], lines[3])
	}
	if h.m.gfx.placed[slotHeader].y != 2 || h.m.gfx.placed[slotBar].y != 21 {
		t.Errorf("placed = %+v", h.m.gfx.placed)
	}
}

// TestCoverOverlayReturns draws a header's image again once the help
// that hid it goes.
func TestCoverOverlayReturns(t *testing.T) {
	h := newHarness(t, albumArt{catalog.NewPlaceholderSource(0)})
	h.withCovers(termimage.Sixel, newCoverHost(t))
	h.m.open(opened{kind: itemAlbum, id: catalog.Placeholder().Albums[0].ID})
	h.covered()
	if got := h.overlays(); !strings.Contains(got, "\x1b[3;2H") {
		t.Fatalf("overlays = %.40q, want the header's", got)
	}
	h.press("?")
	if got := h.overlays(); strings.Contains(got, "\x1b[3;2H") || !strings.Contains(got, "\x1b[22;2H") {
		t.Errorf("over the help, overlays = %.40q, want the bar's alone", got)
	}
	h.press("?")
	if got := h.overlays(); !strings.HasPrefix(got, "\x1b7\x1b[22;2H\x1bP") || !strings.Contains(got, "\x1b[3;2H") {
		t.Errorf("back from the help, overlays = %.40q, want the bar's and the header's", got)
	}
}

// TestCoverOverlayDropped draws no Sixel image over Kitty's placeholders
// once the protocol changed.
func TestCoverOverlayDropped(t *testing.T) {
	h := placeholderHarness(t)
	h.withCovers(termimage.Sixel, newCoverHost(t))
	h.covered()
	gen := h.m.gfx.published
	h.m.gfx.env.Override = termimage.Kitty.String()
	h.send(uv.CellSizeEvent{Width: 10, Height: 20})
	h.covered()
	if got := h.overlays(); got != "" || h.m.gfx.published != gen+1 || strings.Contains(h.m.content, "\x1b^") {
		t.Errorf("over Kitty's placeholders, overlays %.40q, generation %d after %d", got, h.m.gfx.published, gen)
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

	// The Linux console shows no images, and would print the probe.
	g, made = gfx{env: termimage.Env{Term: "linux"}, newArt: g.newArt}, 0
	if cmd := g.start(); cmd != nil {
		t.Error("on the Linux console, start wrote the probe")
	}
	if !g.isReady || g.caps.Protocol != termimage.None || made != 0 {
		t.Errorf("on the Linux console, ready %t, caps = %+v, %d loaders", g.isReady, g.caps, made)
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
	if g.reply(tickMsg{}) {
		t.Error("another message changed the covers")
	}
}

// TestCoverTrim drops covers off screen, the oldest first, once their
// images pass offScreenBytes: placed in another order than their slots.
func TestCoverTrim(t *testing.T) {
	var g gfx
	half := termimage.Encoded{Upload: strings.Repeat("x", offScreenBytes/2)}
	for i, frame := range []uint64{2, 1, 0, 3} {
		g.covers[i] = cover{Encoded: half, key: coverKey{url: strconv.Itoa(i)}, frame: frame}
	}
	g.frame = 3
	g.trim()
	var kept []string
	for _, c := range g.covers {
		if c.Upload != "" {
			kept = append(kept, c.key.url)
		}
	}
	if !slices.Equal(kept, []string{"0", "3"}) {
		t.Errorf("kept %q, want the newest off screen and the one shown", kept)
	}
}

// TestCoverEvicts reuses the cover placed longest ago.
func TestCoverEvicts(t *testing.T) {
	g := gfx{env: termimage.Env{Override: "blocks"}}
	g.detect()
	for i := range numCovers + 2 {
		g.beginFrame()
		g.show(slotBar, 80, 0, catalog.Art(fmt.Sprintf("https://x/%d", i)))
	}
	seen := map[string]bool{}
	for _, c := range g.covers {
		seen[c.key.url] = true
	}
	if seen["https://x/0"] || seen["https://x/1"] || !seen["https://x/9"] {
		t.Errorf("kept %v, want the last %d", seen, numCovers)
	}
}

// TestCoverPlaces shows each protocol's covers from cell 1, after a
// space and before one: on an album's title and subtitle, and on the
// bar's song and progress. A terminal narrower than a cover and
// coverMinWidth shows none, and a short one moves the bar's up.
func TestCoverPlaces(t *testing.T) {
	blank := strings.Repeat("\u2800", 4)
	for _, tc := range []struct {
		p termimage.Protocol
		// cells are a cover's lines, as the screen shows them.
		cells [coverRows]string
		// overlays are drawn over the cells, after the frames that write them.
		isOverlay bool
	}{
		{termimage.Blocks, [coverRows]string{"▀▀▀▀", "▀▀▀▀"}, false},
		{termimage.Kitty, [coverRows]string{}, false},
		{termimage.Sixel, [coverRows]string{blank, blank}, true},
		{termimage.ITerm2, [coverRows]string{blank, blank}, true},
	} {
		t.Run(tc.p.String(), func(t *testing.T) {
			if tc.p == termimage.Kitty {
				for r, line := range placeholders(t, 176) {
					tc.cells[r] = ansi.Strip(line)
				}
			}
			h := newHarness(t, albumArt{catalog.NewPlaceholderSource(0)})
			h.withCovers(tc.p, newCoverHost(t))
			h.m.open(opened{kind: itemAlbum, id: catalog.Placeholder().Albums[0].ID})
			h.covered()
			// at checks that the cover shows from line y, of the screen's
			// lines, beside texts.
			at := func(what string, y int, texts ...string) {
				t.Helper()
				lines := strings.Split(h.screen(), "\n")
				for r, text := range texts {
					if want := " " + tc.cells[r] + " " + text; !strings.HasPrefix(lines[y+r], want) {
						t.Errorf("%s: line %d is %q, want %q first", what, y+r, lines[y+r], want)
					}
				}
			}
			at("the header", 2, "Neon Tide", "Album · ")
			at("the bar", 21, "❙❙ Midnight Arcade", "0:41 ")
			if tc.isOverlay {
				// Cell 1 of lines 2 and 21 is the terminal's (2, 3) and (2, 22).
				if got := h.overlays(); !strings.Contains(got, "\x1b7\x1b[3;2H") || !strings.Contains(got, "\x1b7\x1b[22;2H") {
					t.Errorf("overlays = %.80q, want the header's and the bar's", got)
				}
			}

			// 4 cells and 40: covers show.
			h.resize(44, 24)
			h.covered()
			at("the header, 44 wide", 2, "Neon Tide", "Album · ")
			h.resize(43, 24)
			h.covered()
			lines := strings.Split(h.screen(), "\n")
			if lines[2] != " Neon Tide" || !strings.HasPrefix(lines[21], " ❙❙ Midnight Arcade") {
				t.Errorf("43 wide, the header and the bar show\n%q\n%q", lines[2], lines[21])
			}
			if got := h.overlays(); got != "" {
				t.Errorf("43 wide, drew %.40q", got)
			}

			// The bar's rule cut: its cover on lines 0 and 1, the header's gone.
			h.resize(80, 3)
			h.covered()
			at("the bar, 3 high", 0, "❙❙ Midnight Arcade", "0:41 ")
			if got := h.overlays(); tc.isOverlay && (!strings.HasPrefix(got, "\x1b7\x1b[1;2H") || strings.Count(got, "\x1b7") != 1) {
				t.Errorf("3 high, overlays = %.40q, want the bar's on line 1", got)
			}
			// The song's line cut too: the cover's second line shows, its
			// image does not.
			h.resize(80, 2)
			h.covered()
			if line, _, _ := strings.Cut(h.screen(), "\n"); !strings.HasPrefix(line, " "+tc.cells[1]+" 0:41 ") {
				t.Errorf("2 high, line 0 is %q", line)
			}
			if got := h.overlays(); got != "" {
				t.Errorf("2 high, drew %.40q", got)
			}
		})
	}
}
