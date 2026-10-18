package artwork

import (
	"bytes"
	"errors"
	"flag"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"yt-music-player/internal/budget"
)

var (
	sinkInt    int
	sinkString string
	sinkImage  *Image
	sinkRGBA   *image.RGBA
	sinkColor  color.RGBA
	sinkRect   image.Rectangle
	sinkBytes  []byte
	sinkErr    error
)

// coverPx is the largest decoded cover: a 168 DIP card at 2×.
const coverPx = 352

// coverSizes are what servers send: YouTube Music's largest covers, and
// a playlist's header image.
var coverSizes = []int{544, 1200}

// benchCover is a deterministic n px square with what makes a photo costly
// to code and scale: gradients, shapes and grain.
func benchCover(n int) *image.RGBA { return benchPicture(n, n) }

// benchPicture is benchCover at w×h.
func benchPicture(w, h int) *image.RGBA {
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	seed := uint32(2463534242)
	for y := range h {
		for x := range w {
			seed ^= seed << 13
			seed ^= seed >> 17
			seed ^= seed << 5
			grain := uint8(seed % 24)
			dx, dy := x-w/2, y-h/3
			r := uint8(x*200/w) + grain
			g := uint8(y*160/h) + grain
			b := uint8(120 + (x+y)*100/(w+h))
			if side := min(w, h); dx*dx+dy*dy < side*side/16 {
				r, g, b = 240-grain, 200-grain, 90+grain
			}
			i := m.PixOffset(x, y)
			m.Pix[i], m.Pix[i+1], m.Pix[i+2], m.Pix[i+3] = r, g, b, 255
		}
	}
	return m
}

// benchFile holds benchCover encoded, keyed "format/size", and video
// stills, "jpeg/" and YouTube's name: WebP comes from testdata, as Go has
// no WebP encoder.
var benchFile = sync.OnceValue(func() map[string][]byte {
	files := map[string][]byte{}
	encodeJPEG := func(m image.Image) []byte {
		var j bytes.Buffer
		// Google's thumbnails are JPEGs of about this quality (l90).
		if err := jpeg.Encode(&j, m, &jpeg.Options{Quality: 90}); err != nil {
			panic(err)
		}
		return j.Bytes()
	}
	// hqdefault letterboxes a 16:9 still in 4:3; hq720 is the still.
	still := image.NewRGBA(image.Rect(0, 0, 480, 360))
	draw.Draw(still, image.Rect(0, 45, 480, 315), benchPicture(480, 270), image.Point{}, draw.Src)
	files["jpeg/hqdefault"] = encodeJPEG(still)
	files["jpeg/hq720"] = encodeJPEG(benchPicture(1280, 720))
	for _, n := range coverSizes {
		m := benchCover(n)
		var p bytes.Buffer
		if err := png.Encode(&p, m); err != nil {
			panic(err)
		}
		size := strconv.Itoa(n)
		files["jpeg/"+size], files["png/"+size] = encodeJPEG(m), p.Bytes()
		w, err := os.ReadFile(filepath.Join("testdata", "cover_"+size+".webp"))
		if err != nil {
			panic(err)
		}
		files["webp/"+size] = w
	}
	return files
})

// writeCovers writes benchCover's PNGs, for cwebp to make testdata's WebPs:
//
//	go test -run TestBenchCovers ./internal/artwork/ -write-covers /tmp
//	cwebp -q 80 /tmp/cover_544.png -o internal/artwork/testdata/cover_544.webp
var writeCovers = flag.String("write-covers", "", "write the benchmarks' covers as PNGs to `dir`")

// TestBenchCovers checks testdata's WebP covers have coverSizes.
func TestBenchCovers(t *testing.T) {
	for _, n := range coverSizes {
		if *writeCovers == "" {
			break
		}
		name := filepath.Join(*writeCovers, "cover_"+strconv.Itoa(n)+".png")
		if err := os.WriteFile(name, encodePNG(t, benchCover(n)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range coverSizes {
		size := strconv.Itoa(n)
		cfg, _, err := image.DecodeConfig(bytes.NewReader(benchFile()["webp/"+size]))
		if err != nil || cfg.Width != n || cfg.Height != n {
			t.Errorf("cover_%s.webp: %d×%d, %v", size, cfg.Width, cfg.Height, err)
		}
	}
}

func BenchmarkBucket(b *testing.B) {
	for _, px := range []int{96, 336, 2000} {
		b.Run(strconv.Itoa(px), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkInt = Bucket(px)
			}
		})
	}
}

// sizedURLs are thumbnail URLs as YouTube Music gives them.
var sizedURLs = []struct {
	name, url string
	limit     float64
}{
	// 2 allocs today: url.Parse, and the Builder's.
	{"google", "https://lh3.googleusercontent.com/abc_DEF-123=w544-h544-l90-rj", 2},
	{"ytimg", "https://i.ytimg.com/vi/ZXhwXhv37Ps/hqdefault.jpg", 2}, // 2 allocs today.
	// 1 alloc today: url.Parse's.
	{"ytimg_signed", "https://i.ytimg.com/vi/ZXhwXhv37Ps/hqdefault.jpg?sqp=-oaymwEj&rs=AOn4CLB", 1},
	{"other", "https://example.com/cover.png", 1}, // 1 alloc today, as ytimg_signed.
}

func BenchmarkSizedURL(b *testing.B) {
	for _, tc := range sizedURLs {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkString = SizedURL(tc.url, coverPx)
			}
		})
	}
}

// decodeCases are benchFile's covers at coverPx, and video stills at a
// small row's size. A page holds tens of decoded covers, so their bytes
// are budgeted too.
var decodeCases = []struct {
	name  string
	px    int
	limit budget.Cost
}{
	{"jpeg/544", coverPx, budget.Cost{Allocs: 17, Bytes: 1.16e6}},  // 14 allocs, 1.01 MB today.
	{"jpeg/1200", coverPx, budget.Cost{Allocs: 18, Bytes: 3.13e6}}, // 15 allocs, 2.72 MB today.
	{"jpeg/hqdefault", 64, budget.Cost{Allocs: 19, Bytes: 370e3}},  // 16 allocs, 320 KB today.
	{"jpeg/hq720", 64, budget.Cost{Allocs: 18, Bytes: 1.65e6}},     // 15 allocs, 1.44 MB today.
	// 187 allocs, 1.79 MB today; most allocs are the PNG decoder's.
	{"png/544", coverPx, budget.Cost{Allocs: 216, Bytes: 2.06e6}},
	{"png/1200", coverPx, budget.Cost{Allocs: 666, Bytes: 7.39e6}}, // 579 allocs, 6.43 MB today.
	{"webp/544", coverPx, budget.Cost{Allocs: 29, Bytes: 1.2e6}},   // 25 allocs, 1.05 MB today.
	{"webp/1200", coverPx, budget.Cost{Allocs: 29, Bytes: 3.38e6}}, // 25 allocs, 2.94 MB today.
}

func BenchmarkDecode(b *testing.B) {
	for _, tc := range decodeCases {
		b.Run(tc.name, func(b *testing.B) {
			data := benchFile()[tc.name]
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			for b.Loop() {
				sinkImage, sinkErr = Decode(data, tc.px)
			}
		})
	}
}

// shrinkCases shrink benchCover's RGBA, as a PNG's, to coverPx.
var shrinkCases = []struct {
	n     int
	limit budget.Cost
}{
	// 4 allocs, 528 KB today, for each: the output, the spans and sums.
	{544, budget.Cost{Allocs: 5, Bytes: 610e3}},
	{1200, budget.Cost{Allocs: 5, Bytes: 610e3}},
}

func BenchmarkShrink(b *testing.B) {
	for _, tc := range shrinkCases {
		b.Run(strconv.Itoa(tc.n), func(b *testing.B) {
			m := benchCover(tc.n)
			b.ReportAllocs()
			for b.Loop() {
				sinkRGBA = shrink(m, m.Rect, coverPx)
			}
		})
	}
}

func BenchmarkAverage(b *testing.B) {
	m := benchCover(coverPx)
	b.SetBytes(int64(len(m.Pix)))
	b.ReportAllocs()
	for b.Loop() {
		sinkColor = average(m)
	}
}

// videoStill is a letterboxed 4:3 still, as YouTube's hqdefault.
func videoStill() *image.RGBA {
	return solid(480, 360, color.RGBA{200, 40, 40, 255}, color.RGBA{A: 255})
}

type trimCase struct {
	name  string
	m     image.RGBA64Image
	limit float64
}

// trimCases: a 4:3 still is sampled, a square cover is not.
func trimCases() []trimCase {
	return []trimCase{
		// 0 allocs today: pixels are read through RGBA64At, unboxed.
		{"letterbox", videoStill(), 0},
		// 0 allocs today: a square cover is not a video still.
		{"square", benchCover(544), 0},
	}
}

func BenchmarkTrimBars(b *testing.B) {
	for _, tc := range trimCases() {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkRect = trimBars(tc.m)
			}
		})
	}
}

func coverURL(i int) string {
	return "https://lh3.googleusercontent.com/bench-" + strconv.Itoa(i) + "=w544-h544-l90-rj"
}

// offline fails every request: a miss would make the measured call a fetch.
var offline = &http.Client{Transport: offlineTransport{}}

type offlineTransport struct{}

func (offlineTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("artwork: benchmarks fetch nothing")
}

// offlineLoader makes a Loader that fetches nothing; memory 0 is the default.
func offlineLoader(tb testing.TB, memory int) *Loader {
	tb.Helper()
	l := New(Options{MemoryBytes: memory, Client: offline})
	tb.Cleanup(l.Close)
	return l
}

// heldLoader makes an offline Loader holding n covers of coverPx.
func heldLoader(tb testing.TB, n, memory int) *Loader {
	tb.Helper()
	l := offlineLoader(tb, memory)
	img := &Image{RGBA: image.NewRGBA(image.Rect(0, 0, coverPx, coverPx))}
	for i := range n {
		l.finish(key{coverURL(i), coverPx}, img, nil)
	}
	return l
}

// getCases are what a cover's Get meets each frame: a hit, a load, and a
// load while memory has another size.
var getCases = []struct {
	name  string
	setup func(*Loader) (url string)
}{
	{"hit", func(*Loader) string { return coverURL(0) }},
	{"loading", func(l *Loader) string {
		u := coverURL(1000)
		l.loading[key{u, coverPx}] = true
		return u
	}},
	{"other_size", func(l *Loader) string {
		u := coverURL(1001)
		l.finish(key{u, 192}, &Image{RGBA: image.NewRGBA(image.Rect(0, 0, 192, 192))}, nil)
		l.loading[key{u, coverPx}] = true
		return u
	}},
}

func BenchmarkGet(b *testing.B) {
	for _, tc := range getCases {
		b.Run(tc.name, func(b *testing.B) {
			l := heldLoader(b, 24, defaultMemoryBytes)
			u := tc.setup(l)
			b.ReportAllocs()
			for b.Loop() {
				sinkImage = l.Get(u, coverPx)
			}
		})
	}
}

// frameCases show n covers within the memory budget or over it, when
// NextFrame evicts what is off screen.
var frameCases = []struct {
	name   string
	n      int
	memory int
}{
	{"24", 24, defaultMemoryBytes},
	{"200_over_budget", 200, 8 << 20},
}

// BenchmarkNextFrame is the loader's cost per frame: NextFrame, then a Get
// per cover. URLs are made outside the loop, as the catalog holds them.
func BenchmarkNextFrame(b *testing.B) {
	for _, tc := range frameCases {
		b.Run(tc.name, func(b *testing.B) {
			l := heldLoader(b, tc.n, tc.memory)
			urls := make([]string, tc.n)
			for i := range urls {
				urls[i] = coverURL(i)
			}
			b.ReportAllocs()
			for b.Loop() {
				l.NextFrame()
				for _, u := range urls {
					sinkImage = l.Get(u, coverPx)
				}
			}
		})
	}
}

// evictingLoader fills a Loader's memory with 24 off-screen covers; add
// finishes another load, which evicts the least recently used.
func evictingLoader(tb testing.TB) (add func()) {
	tb.Helper()
	img := &Image{RGBA: image.NewRGBA(image.Rect(0, 0, coverPx, coverPx))}
	l := heldLoader(tb, 24, 24*img.Bytes())
	i := 24
	return func() {
		// Two frames on, the last cover is off screen; URLs repeat only
		// long after eviction.
		l.frame += 2
		l.finish(key{coverURL(i % 64), coverPx}, img, nil)
		i++
	}
}

func BenchmarkFinish(b *testing.B) {
	add := evictingLoader(b)
	b.ReportAllocs()
	for b.Loop() {
		add()
	}
}

func warmDisk(tb testing.TB, u string) *disk {
	tb.Helper()
	d := &disk{dir: tb.TempDir(), maxBytes: defaultDiskBytes}
	d.put(u, benchFile()["jpeg/544"])
	if _, ok := d.get(u); !ok {
		tb.Fatal("the disk cache kept nothing")
	}
	return d
}

// BenchmarkFetch gets a cover's bytes without the network: from disk, or
// from another key's download of the URL.
func BenchmarkFetch(b *testing.B) {
	u := coverURL(0)
	b.Run("disk", func(b *testing.B) {
		l := offlineLoader(b, 0)
		l.disk = warmDisk(b, u)
		b.ReportAllocs()
		for b.Loop() {
			sinkBytes, sinkErr = l.fetch(u)
		}
	})
	b.Run("shared_download", func(b *testing.B) {
		l := sharingLoader(b, u)
		b.ReportAllocs()
		for b.Loop() {
			sinkBytes, sinkErr = l.fetch(u)
		}
	})
}

// cachedFiles is a full disk cache's count: 128 MB of covers of about
// 70 KB.
const cachedFiles = 1800

// fullDisk makes a disk cache of n one-byte files, under its budget.
func fullDisk(tb testing.TB, n int) *disk {
	tb.Helper()
	d := &disk{dir: tb.TempDir(), maxBytes: defaultDiskBytes}
	for i := range n {
		if err := os.WriteFile(d.path(coverURL(i)), []byte{1}, 0o600); err != nil {
			tb.Fatal(err)
		}
	}
	return d
}

// BenchmarkPrune is the scan a run's first write makes, the cache under
// its budget.
func BenchmarkPrune(b *testing.B) {
	b.Run("under_budget", func(b *testing.B) {
		d := fullDisk(b, cachedFiles)
		b.ReportAllocs()
		for b.Loop() {
			d.prune()
		}
	})
}

// sharingLoader makes a Loader whose download of u has finished.
func sharingLoader(tb testing.TB, u string) *Loader {
	tb.Helper()
	l := offlineLoader(tb, 0)
	d := &download{done: make(chan struct{}), data: benchFile()["jpeg/544"]}
	close(d.done)
	l.downloads[u] = d
	return l
}

// memoryTransport answers every request with its bytes, without a socket.
type memoryTransport []byte

func (t memoryTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": {"image/jpeg"}},
		Body: io.NopCloser(bytes.NewReader(t)), ContentLength: int64(len(t)),
	}, nil
}

// BenchmarkLoad is a worker's load end to end, from a server in memory.
func BenchmarkLoad(b *testing.B) {
	data := benchFile()["jpeg/544"]
	l := New(Options{Client: &http.Client{Transport: memoryTransport(data)}})
	b.Cleanup(l.Close)
	k := key{coverURL(0), coverPx}
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		sinkImage, sinkErr = l.load(k)
	}
}
