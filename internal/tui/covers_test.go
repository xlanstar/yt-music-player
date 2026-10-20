package tui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"regexp"
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
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

const coverURL = "https://lh3.googleusercontent.com/cover=w120-h120"

// coverHost serves a red PNG of 128 pixels for every URL, or the PNG of
// pngs for a URL that holds its key, counting the requests; once hold is
// set, after it closes.
type coverHost struct {
	png   []byte
	pngs  map[string][]byte
	hold  chan struct{}
	calls atomic.Int32
}

func newCoverHost(t testing.TB) *coverHost {
	t.Helper()
	return &coverHost{png: colorPNG(t, color.RGBA{220, 30, 40, 255})}
}

// colorPNG is a PNG of 128 pixels of c.
func colorPNG(t testing.TB, c color.RGBA) []byte {
	t.Helper()
	m := image.NewRGBA(image.Rect(0, 0, 128, 128))
	for i := 0; i < len(m.Pix); i += 4 {
		copy(m.Pix[i:], []byte{c.R, c.G, c.B, c.A})
	}
	var b bytes.Buffer
	if err := png.Encode(&b, m); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func (c *coverHost) RoundTrip(r *http.Request) (*http.Response, error) {
	c.calls.Add(1)
	if c.hold != nil {
		<-c.hold
	}
	b := c.png
	for k, png := range c.pngs {
		if strings.Contains(r.URL.String(), k) {
			b = png
		}
	}
	return &http.Response{
		StatusCode:    http.StatusOK,
		Body:          io.NopCloser(bytes.NewReader(b)),
		ContentLength: int64(len(b)),
		Header:        http.Header{},
	}, nil
}

// withCovers shows covers as YTM_IMAGES=p says, of 10×20-pixel cells, the
// current song's from host.
func (h *harness) withCovers(p termimage.Protocol, host *coverHost) {
	h.t.Helper()
	h.m.probe, h.m.covers = coverGfx(p, host, h.m.posts.post)
	want := []string{termimage.CellSizeQuery}
	if p == termimage.None {
		want = nil
	}
	if raws := h.run(h.m.probe.start()); !slices.Equal(raws, want) {
		h.t.Fatalf("an override asked %q, want only the cell's pixels", raws)
	}
	h.t.Cleanup(func() { h.m.covers.close() })
	song := h.mirror.songs[h.mirror.state.Current]
	song.Art = coverURL
	h.mirror.songs[song.ID] = song
}

// coverGfx shows covers as YTM_IMAGES=p says, of 10×20-pixel cells, from
// host; post runs on the UI thread what its loads and encodes bring.
func coverGfx(p termimage.Protocol, host *coverHost, post func(func())) (probe, covers) {
	return probe{
		env:     termimage.Env{Override: p.String()},
		replies: termimage.Replies{CellW: 10, CellH: 20},
	}, covers{
		kittyBase: 176,
		post:      post,
		newArt: func() *artwork.Loader {
			return artwork.New(artwork.Options{
				Post: post, Client: &http.Client{Transport: host}, MemoryBytes: artMemory, Workers: 1,
			})
		},
	}
}

// covered settles until the page and the covers shown loaded and encoded,
// and returns what went to the terminal meanwhile.
func (h *harness) covered() []string {
	h.t.Helper()
	raws := h.run(h.send(postedMsg{}))
	deadline := time.After(5 * time.Second)
	for h.m.covers.isBusy() || h.m.loader.IsBusy() {
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
	g := &h.m.covers
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
	e, err := termimage.Encode(m, termimage.Caps{Protocol: termimage.Kitty}, 4, 2, id, false)
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
	h.m.covers.kittyBase = 40
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
	h.m.covers.cache[0] = cover{}
	if got := h.m.covers.close(); !strings.HasPrefix(got, termimage.KittyDelete(40, false)) ||
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
			gen, content := h.m.covers.published, h.m.content
			h.press("tab")
			h.press("shift+tab")
			if raws := h.run(h.send(tea.WindowSizeMsg{Width: 90, Height: 24})); len(raws) != 1 || raws[0] != termimage.CellSizeQuery {
				t.Errorf("a resize wrote %.40q, want the cell's size asked", raws)
			}
			h.resize(80, 24)
			if h.m.covers.published != gen || h.m.content != content {
				t.Errorf("generation %d after %d, the screen the same %t", h.m.covers.published, gen, h.m.content == content)
			}
			// Without the cover, its cells are painted over: nothing to draw.
			h.mirror.setQueue(nil)
			h.send(postedMsg{})
			if got := h.overlays(); got != "" || h.m.covers.published != gen+1 || strings.Contains(h.m.content, "\x1b^") {
				t.Errorf("without a cover, overlays %q, generation %d after %d", got, h.m.covers.published, gen)
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
	if c := h.m.covers.ready(h.drawnCover(slotBar).key(h.m.probe.caps)); uploads != 2 || c == nil || c.srcPx != 80 {
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
	// The header's claims the first place.
	if got := h.m.scene.covers; len(got) != 2 || got[0].rect != image.Rect(1, 2, 5, 4) || got[0].slot != slotHeader ||
		got[1].rect != image.Rect(1, 21, 5, 23) {
		t.Errorf("covers = %+v", got)
	}
}

// drawnCover is the cover the last render drew in slot s.
func (h *harness) drawnCover(s slot) coverRequest {
	h.t.Helper()
	for _, r := range h.m.scene.covers {
		if r.slot == s {
			return r
		}
	}
	h.t.Fatalf("no cover in slot %d", s)
	return coverRequest{}
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
	gen := h.m.covers.published
	h.m.probe.env.Override = termimage.Kitty.String()
	h.send(uv.CellSizeEvent{Width: 10, Height: 20})
	h.covered()
	if got := h.overlays(); got != "" || h.m.covers.published != gen+1 || strings.Contains(h.m.content, "\x1b^") {
		t.Errorf("over Kitty's placeholders, overlays %.40q, generation %d after %d", got, h.m.covers.published, gen)
	}
}

func TestCoverOff(t *testing.T) {
	host := newCoverHost(t)
	h := placeholderHarness(t)
	h.withCovers(termimage.None, host)
	before := h.screen()
	h.covered()
	if h.m.covers.art != nil || host.calls.Load() != 0 {
		t.Error("covers off made a loader or fetched")
	}
	if h.screen() != before {
		t.Error("covers off changed the screen")
	}
}

// TestCoverTrim drops covers off screen, the oldest first, once their
// images pass offScreenBytes: placed in another order than their slots.
func TestCoverTrim(t *testing.T) {
	var g covers
	half := termimage.Encoded{Upload: strings.Repeat("x", offScreenBytes/2)}
	for i, frame := range []uint64{2, 1, 0, 3} {
		g.cache[i] = cover{Encoded: half, key: coverKey{url: strconv.Itoa(i)}, frame: frame}
	}
	g.frame = 3
	g.trim()
	var kept []string
	for _, c := range g.cache {
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
	pr := probe{env: termimage.Env{Override: "blocks"}}
	pr.detect()
	var g covers
	for i := range numCovers + 2 {
		r := coverRequest{rect: image.Rect(1, 0, 5, 2), art: catalog.Art(fmt.Sprintf("https://x/%d", i))}
		g.sync([]coverRequest{r}, pr.caps, 80, 24)
	}
	seen := map[string]bool{}
	for _, c := range g.cache {
		seen[c.key.url] = true
	}
	if seen["https://x/0"] || seen["https://x/1"] || !seen["https://x/9"] {
		t.Errorf("kept %v, want the last %d", seen, numCovers)
	}
}

// TestCoverPlaces shows each protocol's covers from cell 1, after a
// space and before one: on an album's title and subtitle, and on the
// bar's song and progress; none narrower than a cover and coverMinWidth.
// A short terminal moves the bar's up, blank once cut.
func TestCoverPlaces(t *testing.T) {
	blank := strings.Repeat("\u2800", 4)
	for _, tc := range []struct {
		p termimage.Protocol
		// cells are a cover's lines, as the screen shows them.
		cells [2]string
		// overlays are drawn over the cells, after the frames that write them.
		isOverlay bool
	}{
		{termimage.Blocks, [2]string{"▀▀▀▀", "▀▀▀▀"}, false},
		{termimage.Kitty, [2]string{}, false},
		{termimage.Sixel, [2]string{blank, blank}, true},
		{termimage.ITerm2, [2]string{blank, blank}, true},
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
			// The song's line cut too: a cover cut shows blank, where it
			// was.
			h.resize(80, 2)
			h.covered()
			if line, _, _ := strings.Cut(h.screen(), "\n"); !strings.HasPrefix(line, strings.Repeat(" ", 6)+"0:41 ") {
				t.Errorf("2 high, line 0 is %q", line)
			}
			if got := h.overlays(); got != "" {
				t.Errorf("2 high, drew %.40q", got)
			}
		})
	}
}

// startCovers is coverGfx started, closed when t ends, its posts run when
// the test says.
func startCovers(t *testing.T, p termimage.Protocol, host *coverHost) (*probe, *covers, *poster) {
	t.Helper()
	posts := newPoster()
	pr, g := coverGfx(p, host, posts.post)
	pr.start()
	t.Cleanup(func() { g.close() })
	return &pr, &g, posts
}

// showFrame readies reqs as an 80×24 frame's covers and commits them; it
// returns what they show and Kitty's uploads.
func showFrame(g *covers, caps termimage.Caps, reqs ...coverRequest) (coverFrame, string) {
	f := g.sync(reqs, caps, 80, 24)
	if cmd := g.commit(); cmd != nil {
		return f, fmt.Sprint(cmd().(tea.RawMsg).Msg)
	}
	return f, ""
}

// settleCovers shows reqs frame after frame until they loaded and
// encoded, and returns Kitty's uploads meanwhile.
func settleCovers(t *testing.T, g *covers, caps termimage.Caps, posts *poster, reqs ...coverRequest) string {
	t.Helper()
	var uploads strings.Builder
	deadline := time.After(5 * time.Second)
	for {
		_, sent := showFrame(g, caps, reqs...)
		uploads.WriteString(sent)
		if !g.isBusy() {
			return uploads.String()
		}
		select {
		case <-posts.wake:
			posts.run()
		case <-deadline:
			t.Fatal("covers never encoded")
		}
	}
}

// mustRequest is art's cover in slot s, rows tall from line y of an
// 80-cell screen.
func mustRequest(t *testing.T, g *probe, s slot, y, rows int, art catalog.Art) coverRequest {
	t.Helper()
	r, ok := g.request(s, y, rows, 80, art)
	if !ok {
		t.Fatalf("no cover %d rows tall", rows)
	}
	return r
}

// blankCover is a cover's lines while it loads.
func blankCover(cols, rows int) []string {
	return slices.Repeat([]string{strings.Repeat(" ", cols)}, rows)
}

// TestCoverSizes keeps one image at two sizes as two covers, each blank
// at its own size while it loads, then encoded at it.
func TestCoverSizes(t *testing.T) {
	host := newCoverHost(t)
	host.hold = make(chan struct{})
	pr, g, posts := startCovers(t, termimage.Sixel, host)
	header := mustRequest(t, pr, slotHeader, 2, 3, coverURL)
	bar := mustRequest(t, pr, slotBar, 21, 2, coverURL)
	f, _ := showFrame(g, pr.caps, header, bar)
	if got := f.lines[slotHeader]; !slices.Equal(got, blankCover(6, 3)) {
		t.Errorf("the header's blank is %q", got)
	}
	if got := f.lines[slotBar]; !slices.Equal(got, blankCover(4, 2)) {
		t.Errorf("the bar's blank is %q", got)
	}
	close(host.hold)
	settleCovers(t, g, pr.caps, posts, header, bar)
	f, _ = showFrame(g, pr.caps, header, bar)
	for _, tc := range []struct {
		r          coverRequest
		cols, rows int
		raster     string
	}{
		{header, 6, 3, "\"1;1;60;60#"},
		{bar, 4, 2, "\"1;1;40;36#"},
	} {
		c := g.ready(tc.r.key(pr.caps))
		if c == nil {
			t.Fatalf("%d×%d not encoded", tc.cols, tc.rows)
		}
		lines := slices.Repeat([]string{strings.Repeat("\u2800", tc.cols)}, tc.rows)
		if got := f.lines[tc.r.slot]; !slices.Equal(got, lines) || !strings.Contains(c.Overlay, tc.raster) {
			t.Errorf("%d×%d: lines %q, overlay %.30q", tc.cols, tc.rows, got, c.Overlay)
		}
	}
	if g.ready(header.key(pr.caps)) == g.ready(bar.key(pr.caps)) {
		t.Error("one cover holds both sizes")
	}
}

// TestCoverResizeStaleJob shows the size asked last: a cell resize while
// an encode runs leaves the old size's result to its own cover, and the
// new size shows blank until its own comes.
func TestCoverResizeStaleJob(t *testing.T) {
	h := placeholderHarness(t)
	h.withCovers(termimage.Sixel, newCoverHost(t))
	g := &h.m.covers
	encoding := func(cols int) bool {
		for i := range g.cache {
			if c := &g.cache[i]; c.key.cols == cols && c.job.IsRunning() {
				return true
			}
		}
		return false
	}
	h.send(postedMsg{})
	deadline := time.After(5 * time.Second)
	for !encoding(4) {
		select {
		case <-h.m.posts.wake:
			h.send(postedMsg{})
		case <-deadline:
			t.Fatal("never encoded")
		}
	}
	// The bar's song is line 21, its cover 4 cells, blank meanwhile.
	songLine := func() string { return strings.Split(h.screen(), "\n")[21] }
	if line := songLine(); !strings.HasPrefix(line, strings.Repeat(" ", 6)+"❙❙") {
		t.Fatalf("loading, the bar is %q", line)
	}
	// Cells of 16×20: covers 3 cells wide. The 4's result waits.
	h.send(uv.CellSizeEvent{Width: 16, Height: 20})
	if line := songLine(); !strings.HasPrefix(line, strings.Repeat(" ", 5)+"❙❙") {
		t.Errorf("resized while loading, the bar is %q, want 3 blank cells", line)
	}
	h.covered()
	if line := songLine(); !strings.HasPrefix(line, " \u2800\u2800\u2800 ❙❙") {
		t.Errorf("encoded, the bar is %q, want 3 cells", line)
	}
	if got := h.overlays(); !strings.HasPrefix(got, "\x1b7\x1b[22;2H\x1bP0;1;0q\"1;1;48;36#") || strings.Count(got, "\x1b7") != 1 {
		t.Errorf("overlays = %.60q, want the 3 cells'", got)
	}
	for i := range g.cache {
		c := &g.cache[i]
		if c.isReady && !strings.Contains(c.Overlay, fmt.Sprintf("\"1;1;%d;", c.key.cols*c.key.caps.CellW)) {
			t.Errorf("the cover of %d cells of %d pixels holds %.30q", c.key.cols, c.key.caps.CellW, c.Overlay)
		}
	}
}

// TestCoverStaleJob drops an encode's result once its cover was taken
// for another image, of another size, while it ran: the cover shows its
// own blank, then its own image, whichever result comes first.
func TestCoverStaleJob(t *testing.T) {
	posted := make(chan func(), 2)
	pr := probe{env: termimage.Env{Override: "sixel"}, replies: termimage.Replies{CellW: 10, CellH: 20}}
	pr.detect()
	g := covers{post: func(fn func()) { posted <- fn }}
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	a := coverKey{url: "https://x/a", caps: pr.caps, cols: 4, rows: 2}
	b := coverKey{url: "https://x/b", caps: pr.caps, cols: 6, rows: 3}
	// encodingA returns the cover, the oldest, that encodes a, and a's
	// result, waiting to run.
	encodingA := func() (*cover, func()) {
		g.cache = [numCovers]cover{}
		i := g.claim(a)
		for k := range g.cache {
			g.cache[k].frame = 1
		}
		c := &g.cache[i]
		c.frame = 0
		g.encode(c, img, 0)
		return c, <-posted
	}

	c, aDone := encodingA()
	if g.claim(b); c.key != b {
		t.Fatalf("b took cover %+v, want a's", c.key)
	}
	aDone()
	if c.isReady || c.Overlay != "" || !slices.Equal(c.Lines, blankCover(6, 3)) {
		t.Errorf("taken by b, the cover shows %q, want b's blank", c.Lines)
	}
	for _, isAFirst := range []bool{true, false} {
		c, aDone := encodingA()
		g.claim(b)
		g.encode(c, img, 0)
		bDone := <-posted
		if isAFirst {
			aDone()
			bDone()
		} else {
			bDone()
			aDone()
		}
		if c.key != b || !c.isReady || !strings.Contains(c.Overlay, "\"1;1;60;60#") || len(c.Lines) != 3 {
			t.Errorf("a's result first %t: the cover holds %.30q, %d lines; want b's", isAFirst, c.Overlay, len(c.Lines))
		}
	}
}

// kittyUpload matches an upload's image ID and cells.
var kittyUpload = regexp.MustCompile(`,i=(\d+),c=(\d+),r=(\d+),`)

// TestCoverKittyIDs gives each cover its place's image ID whatever its
// size: one image at two sizes is two images, and a place taken for
// another cover uploads it under the same ID, replacing the image no
// longer shown.
func TestCoverKittyIDs(t *testing.T) {
	pr, g, posts := startCovers(t, termimage.Kitty, newCoverHost(t))
	// uploaded is each ID's last upload's cells.
	uploaded := map[uint8][2]int{}
	for n := range numCovers + 3 {
		art := catalog.Art(fmt.Sprintf("https://lh3.googleusercontent.com/cover%d=w120-h120", n))
		reqs := []coverRequest{mustRequest(t, pr, slotHeader, 2, 3, art), mustRequest(t, pr, slotBar, 21, 2, art)}
		for _, m := range kittyUpload.FindAllStringSubmatch(settleCovers(t, g, pr.caps, posts, reqs...), -1) {
			id, _ := strconv.Atoi(m[1])
			cols, _ := strconv.Atoi(m[2])
			rows, _ := strconv.Atoi(m[3])
			uploaded[uint8(id)] = [2]int{cols, rows}
		}
		f, _ := showFrame(g, pr.caps, reqs...)
		var ids [2]uint8
		for k, r := range reqs {
			i := slices.IndexFunc(g.cache[:], func(c cover) bool { return c.key == r.key(pr.caps) })
			if i < 0 {
				t.Fatalf("cover %d: no place holds %+v", n, r.key(pr.caps))
			}
			id := g.kittyBase + uint8(i)
			ids[k] = id
			cols, rows := r.rect.Dx(), r.rect.Dy()
			e, err := termimage.Encode(image.NewRGBA(image.Rect(0, 0, 1, 1)), pr.caps, cols, rows, id, false)
			if err != nil {
				t.Fatal(err)
			}
			if got := f.lines[r.slot]; !slices.Equal(got, e.Lines) {
				t.Errorf("cover %d, %d×%d: shows another image than %d", n, cols, rows, id)
			}
			if uploaded[id] != [2]int{cols, rows} {
				t.Errorf("cover %d, %d×%d: image %d's last upload is %v", n, cols, rows, id, uploaded[id])
			}
		}
		if ids[0] == ids[1] {
			t.Errorf("cover %d: both sizes are image %d", n, ids[0])
		}
	}
	for id := range uploaded {
		if id < g.kittyBase || id >= g.kittyBase+numCovers {
			t.Errorf("uploaded image %d, past the %d places from %d", id, numCovers, g.kittyBase)
		}
	}
}

// drawCovers draws reqs into an 80×24 scene, text after each line of
// theirs, readies and commits them, and returns the screen composed.
func drawCovers(g *covers, caps termimage.Caps, reqs ...coverRequest) string {
	s := &scene{}
	screen := s.reset(80, 24, newCells(ansi.WcWidth))
	for _, r := range reqs {
		screen.cover(r.rect, r.slot, r.art)
		for y := r.rect.Min.Y; y < r.rect.Max.Y; y++ {
			screen.text(r.rect.Max.X+1, y, "text", paint{})
		}
	}
	f := g.sync(s.covers, caps, 80, 24)
	g.commit()
	return s.compose(&f)
}

// TestCoverGenStable keeps the overlays' table, its generation and the
// marks' bytes while covers of any size stay put and wholly on screen,
// above its last line; a change makes the next generation.
func TestCoverGenStable(t *testing.T) {
	pr, g, posts := startCovers(t, termimage.Sixel, newCoverHost(t))
	header := mustRequest(t, pr, slotHeader, 2, 3, coverURL)
	bar := mustRequest(t, pr, slotBar, 21, 2, coverURL)
	settleCovers(t, g, pr.caps, posts, header, bar)
	screen := drawCovers(g, pr.caps, header, bar)
	table, gen := g.table, g.published
	for range 3 {
		if drawCovers(g, pr.caps, header, bar) != screen {
			t.Error("unchanged, the screen changed")
		}
	}
	if g.table != table || g.published != gen {
		t.Errorf("unchanged, generation %d after %d", g.published, gen)
	}
	if p := g.places[slotHeader]; p.X != 1 || p.Y != 2 || p.Cols != 6 || p.Rows != 3 {
		t.Errorf("the header's overlay is at %d,%d, %d×%d; want 1,2, 6×3", p.X, p.Y, p.Cols, p.Rows)
	}
	if p := g.places[slotBar]; p.X != 1 || p.Y != 21 || p.Cols != 4 || p.Rows != 2 {
		t.Errorf("the bar's overlay is at %d,%d, %d×%d; want 1,21, 4×2", p.X, p.Y, p.Cols, p.Rows)
	}
	// marked checks that the screen's lines carry the marks of gen at the
	// overlays' first cells, and only there.
	marked := func(screen string, gen uint64, overlays ...coverRequest) {
		t.Helper()
		for y, line := range strings.Split(screen, "\n") {
			want := ""
			for _, r := range overlays {
				if y >= r.rect.Min.Y && y < r.rect.Max.Y {
					want = " " + termimage.Mark(gen, int(r.slot), y-r.rect.Min.Y) + "\u2800"
				}
			}
			if got := strings.Count(line, "\x1b^"); want == "" && got != 0 || want != "" && !strings.HasPrefix(line, want) {
				t.Errorf("line %d is %q, want %q first", y, line, want)
			}
		}
	}
	marked(screen, gen, header, bar)

	// Its top cut, the bar's cover draws no image.
	cut := bar
	cut.rect = cut.rect.Sub(image.Pt(0, 22))
	marked(drawCovers(g, pr.caps, header, cut), gen+1, header)
	if g.published != gen+1 || g.places[slotBar] != (termimage.Placement{}) || g.places[slotHeader].Seq == "" {
		t.Errorf("the bar's cover cut, generation %d after %d, places %+v", g.published, gen, g.places)
	}
	// On the last line, it shows its cells, blank, but no image, which
	// would scroll a Sixel terminal.
	low := bar
	low.rect = low.rect.Add(image.Pt(0, 1))
	screen = drawCovers(g, pr.caps, header, low)
	marked(screen, gen+1, header)
	if line := strings.Split(screen, "\n")[23]; line != " "+strings.Repeat("\u2800", 4)+" text" {
		t.Errorf("the last line is %q, want the cover's cells", line)
	}
	if g.published != gen+1 || g.places[slotBar] != (termimage.Placement{}) {
		t.Errorf("the bar's cover on the last line, generation %d after %d, places %+v", g.published, gen, g.places)
	}
	marked(drawCovers(g, pr.caps, header, bar), gen+2, header, bar)
	if g.published != gen+2 || g.places[slotBar].Y != 21 {
		t.Errorf("the bar's cover back, generation %d after %d", g.published, gen)
	}
}

// TestCoverTrimSizes trims each size of an image as a cover of its own:
// the size not shown goes, the one shown stays.
func TestCoverTrimSizes(t *testing.T) {
	var g covers
	half := termimage.Encoded{Overlay: strings.Repeat("x", offScreenBytes/2)}
	for i, k := range []struct {
		cols, rows int
		frame      uint64
	}{{4, 2, 3}, {6, 3, 1}, {2, 1, 2}} {
		g.cache[i] = cover{Encoded: half, key: coverKey{url: coverURL, cols: k.cols, rows: k.rows}, frame: k.frame}
	}
	g.frame = 3
	g.trim()
	var kept []int
	for _, c := range g.cache {
		if c.Overlay != "" {
			kept = append(kept, c.key.cols)
		}
	}
	if !slices.Equal(kept, []int{4, 2}) {
		t.Errorf("kept the covers %v cells wide, want 4 (shown) and 2", kept)
	}
}
