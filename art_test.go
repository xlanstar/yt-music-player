package main

import (
	"bytes"
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"yt-music-player/internal/artwork"
	"yt-music-player/internal/catalog"

	"github.com/egoist/mygo/ui"
	"github.com/rs/zerolog"
)

func TestMain(m *testing.M) {
	// Tests load thumbnails from their own servers, never the user's cache.
	artCacheDir = ""
	// Otherwise tests print every failure they provoke.
	zerolog.SetGlobalLevel(zerolog.Disabled)
	os.Exit(m.Run())
}

var (
	green = color.RGBA{0, 200, 60, 255}
	blue  = color.RGBA{30, 60, 220, 255}
)

// imageServer serves 400 px PNGs: /green.png and /blue.png in their
// colors, 404 for others.
type imageServer struct {
	*httptest.Server
	mu   sync.Mutex
	hits map[string]int
}

func newImageServer(t testing.TB) *imageServer {
	s := &imageServer{hits: map[string]int{}}
	pngs := map[string][]byte{}
	for path, c := range map[string]color.RGBA{"/green.png": green, "/blue.png": blue} {
		m := image.NewRGBA(image.Rect(0, 0, 400, 400))
		for i := 0; i < len(m.Pix); i += 4 {
			copy(m.Pix[i:], []byte{c.R, c.G, c.B, c.A})
		}
		var b bytes.Buffer
		if err := png.Encode(&b, m); err != nil {
			t.Fatal(err)
		}
		pngs[path] = b.Bytes()
	}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits[r.URL.Path]++
		s.mu.Unlock()
		data, ok := pngs[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *imageServer) count(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[path]
}

// pixels counts the last frame's pixels close to color c.
func pixels(tt *ui.Tester, c color.RGBA) int {
	frame := tt.Image()
	n := 0
	near := func(a, b uint8) bool { return a-b < 4 || b-a < 4 }
	for i := 0; i+3 < len(frame.Pix); i += 4 {
		if near(frame.Pix[i], c.R) && near(frame.Pix[i+1], c.G) && near(frame.Pix[i+2], c.B) {
			n++
		}
	}
	return n
}

// The thumbnail is fetched once, at twice its shown size: a sized cover's
// at once, a stretched one's once measured in the first frame.
func TestCoverShowsThumbnailOnceLoaded(t *testing.T) {
	s := newImageServer(t)
	for _, tc := range []struct {
		name, path string
		col        color.RGBA
		w, side    float32 // w 0 stretches the cover to side
		builds     int     // until first shown
	}{
		{"sized", "/green.png", green, 100, 100, 1},
		{"stretched", "/blue.png", blue, 0, 160, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, q := testApp()
			art := catalog.Art(s.URL + tc.path)
			builds := 0
			tt := ui.NewTester(func(c *ui.Context) {
				builds++
				a.useArt()
				ui.Box(c).Fill().Center().Children(func() {
					ui.Column(c).Width(tc.side).Children(func() { coverArt(c, art, tc.w, tc.w, 12) })
				})
			}, 300, 300)
			// The URL's generated cover, while it loads.
			if n := pixels(tt, tc.col); n != 0 || builds != tc.builds {
				t.Fatalf("%d pixels of the thumbnail before it loaded, first shown in %d builds", n, builds)
			}
			waitPost(t, tt, q)
			// The whole cover but its rounded corners.
			if n := pixels(tt, tc.col); n < int(tc.side*tc.side)-200 || n > int(tc.side*tc.side) {
				t.Errorf("%d pixels of the thumbnail once loaded", n)
			}
			if from := coverOf(art).from; from != ui.RGB(tc.col.R, tc.col.G, tc.col.B) {
				t.Errorf("glow color %v, want the image's average", from)
			}
			// Nothing animates, and nothing more is posted or fetched.
			builds = 0
			tt.Frame()
			if builds != 1 {
				t.Errorf("a frame built %d times", builds)
			}
			select {
			case <-q:
				t.Error("posted again")
			case <-time.After(50 * time.Millisecond):
			}
			if n := s.count(tc.path); n != 1 {
				t.Errorf("fetched %d times", n)
			}
			px := int(2 * tc.side)
			if img := a.art.Get(string(art), px); img == nil || img.RGBA.Bounds().Dx() != artwork.Bucket(px) {
				t.Errorf("thumbnail %v, want %d px", img, px)
			}
		})
	}
}

func TestLoadingCovers(t *testing.T) {
	// Loading covers differ by URL, and from the plain one of unknown art.
	art := catalog.Art("https://example.com/a.png")
	if cv, other := urlCover(art), urlCover(art+"?v=2"); cv == other || cv == coverOf("unknown") || cv != urlCover(art) {
		t.Errorf("loading covers %v and %v", cv, other)
	}
}

func TestSearchResultsShowThumbnails(t *testing.T) {
	s := newImageServer(t)
	src, a, q := scriptedApp()
	tt := newTester(a)
	a.router.Push(searchPath("far"))
	songs := []catalog.Song{
		{ID: "far-1", Title: "Carrier Wave", Artist: "Nova Static", Seconds: 201, Art: catalog.Art(s.URL + "/green.png")},
		{ID: "far-2", Title: "Dead Air", Artist: "Nova Static", Seconds: 187, Art: catalog.Art(s.URL + "/missing.png")},
		{ID: "far-3", Title: "Low Band", Artist: "Nova Static", Seconds: 187, Art: catalog.Art(s.URL + "/blue.png")},
	}
	src.Next(t, tt.Frame, "search", "far").Reply <- reply{Results: catalog.Results{Songs: songs}}
	waitPost(t, tt, q)
	if !tt.HasText("Dead Air") {
		t.Fatalf("no results, texts %q", tt.Texts())
	}
	if pixels(tt, green) != 0 || pixels(tt, blue) != 0 {
		t.Fatal("thumbnails shown before they loaded")
	}
	// Loads that finish together redraw once; give them all time, and
	// the Tester's clock the time to fade them in.
	deadline := time.Now().Add(5 * time.Second)
	for tt.Advance(time.Second); pixels(tt, green) == 0 || pixels(tt, blue) == 0; tt.Advance(time.Second) {
		if time.Now().After(deadline) {
			t.Fatalf("thumbnails not shown: %d green, %d blue pixels", pixels(tt, green), pixels(tt, blue))
		}
		waitPost(t, tt, q)
	}
	for range 10 {
		tt.Frame()
	}
	// Each image once per size it shows at (the top result's and a row's),
	// however many frames showed it; the missing one isn't retried every
	// frame.
	for _, p := range []string{"/green.png", "/blue.png", "/missing.png"} {
		if s.count(p) > 2 {
			t.Errorf("%s fetched %d times", p, s.count(p))
		}
	}
	if !strings.Contains(string(songs[1].Art), "missing") || coverOf(songs[1].Art) != urlCover(songs[1].Art) {
		t.Error("a missing thumbnail lost its generated cover")
	}
	// Closing the loader, as the window does, stops loading.
	a.art.Close()
	if a.art.Get(s.URL+"/other.png", 96) != nil {
		t.Error("loaded after closing")
	}
}

// thumbnailServer serves a 544 px PNG, YouTube Music's largest cover size,
// at every path, colored by the path; it lives as long as the tests.
var thumbnailServer = sync.OnceValue(func() *httptest.Server {
	var pngs sync.Map
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := pngs.Load(r.URL.Path)
		if !ok {
			h := fnv.New32a()
			_, _ = h.Write([]byte(r.URL.Path))
			sum := h.Sum32()
			m := image.NewRGBA(image.Rect(0, 0, 544, 544))
			for i := 0; i < len(m.Pix); i += 4 {
				// A gradient, so that scaling has work to do.
				x := uint8(i / 4 % 544 * 255 / 544)
				copy(m.Pix[i:], []byte{uint8(sum), uint8(sum>>8) ^ x, uint8(sum >> 16), 255})
			}
			var b bytes.Buffer
			if err := png.Encode(&b, m); err != nil {
				panic(err)
			}
			data, _ = pngs.LoadOrStore(r.URL.Path, b.Bytes())
		}
		_, _ = w.Write(data.([]byte))
	}))
})

func thumbArt(art catalog.Art) catalog.Art {
	if art == "" {
		return ""
	}
	return catalog.Art(thumbnailServer().URL + "/" + string(art) + ".png")
}

// thumbnailCatalog puts every placeholder art on thumbnailServer but the
// playing first song's, for thumbSource's reason.
func thumbnailCatalog() *catalog.Catalog {
	small := catalog.Placeholder()
	songs := slices.Clone(small.Songs)
	for i := range songs[1:] {
		songs[i+1].Art = thumbArt(songs[i+1].Art)
	}
	albums := thumbAlbums(slices.Clone(small.Albums))
	playlists := slices.Clone(small.Playlists)
	for i := range playlists {
		playlists[i].Art = thumbArt(playlists[i].Art)
	}
	c := catalog.New(songs, albums, playlists)
	c.SetLibrary(libraryIDs(small))
	return c
}
