package artwork

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// server serves one PNG at every path but those containing "missing", and
// counts requests per path. It is in memory, so that tests run in a
// synctest bubble wait with synctest.Wait rather than poll.
type server struct {
	*httptest.Server
	mu   sync.Mutex
	hits map[string]int
	// gate, unless nil, holds every response until closed.
	gate chan struct{}
	png  []byte
}

// coverPNG is made once: encoding is slow under the race detector.
var coverPNG = sync.OnceValue(func() []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, solid(600, 600, color.RGBA{10, 120, 200, 255}, color.RGBA{})); err != nil {
		panic(err)
	}
	return b.Bytes()
})

func newServer(t *testing.T) *server {
	s := &server{hits: map[string]int{}, png: coverPNG()}
	s.Server = httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits[r.URL.Path]++
		gate := s.gate
		s.mu.Unlock()
		if gate != nil {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		if strings.Contains(r.URL.Path, "missing") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(s.png)
	}))
	s.Client() // sets s.URL
	return s
}

func (s *server) count(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[path]
}

// posts collects what the loader posts, for tests to run as the UI thread.
type posts chan func()

func newLoader(t *testing.T, s *server, opts Options) (*Loader, posts) {
	q := make(posts, 64)
	opts.Client = s.Client()
	opts.Post = func(fn func()) { q <- fn }
	l := New(opts)
	t.Cleanup(l.Close)
	return l, q
}

// wait runs the post the loader made once its goroutines are blocked.
func (q posts) wait(t *testing.T) {
	t.Helper()
	synctest.Wait()
	select {
	case fn := <-q:
		fn()
	default:
		t.Fatal("no post")
	}
}

func (q posts) none(t *testing.T) {
	t.Helper()
	synctest.Wait()
	select {
	case <-q:
		t.Fatal("posted again")
	default:
	}
}

// waitIdle fails unless every load is done once the loader's goroutines
// are blocked.
func waitIdle(t *testing.T, l *Loader) {
	t.Helper()
	synctest.Wait()
	l.mu.Lock()
	busy := len(l.loading) > 0 || l.workers > 0
	l.mu.Unlock()
	if busy {
		t.Fatal("still loading")
	}
}

func TestGetLoadsOnceAndPostsOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newServer(t)
		prepared := 0
		l, q := newLoader(t, s, Options{Prepare: func(m *image.RGBA) any { prepared++; return m.Bounds() }})
		u := s.URL + "/cover.png"
		s.mu.Lock()
		s.gate = make(chan struct{})
		s.mu.Unlock()
		// As every build asks while it loads, at sizes of one bucket.
		for _, px := range []int{100, 104, 112, 112} {
			if img := l.Get(u, px); img != nil {
				t.Fatal("loaded at once")
			}
		}
		close(s.gate)
		q.wait(t)
		img := l.Get(u, 112)
		if img == nil {
			t.Fatal("not loaded after its post")
		}
		if b := img.RGBA.Bounds(); b.Dx() != 112 || b.Dy() != 112 {
			t.Errorf("bounds %v, want 112 square", b)
		}
		if img.UI != img.RGBA.Bounds() || prepared != 1 {
			t.Errorf("UI %v, prepared %d times", img.UI, prepared)
		}
		if avg, ok := l.Average(u); !ok || avg != img.Average || !near(avg, color.RGBA{10, 120, 200, 255}, 2) {
			t.Errorf("average %v %v, image's %v", avg, ok, img.Average)
		}
		if n := s.count("/cover.png"); n != 1 {
			t.Errorf("fetched %d times", n)
		}
		q.none(t)
	})
}

func TestKeysOfOneURLShareADownload(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newServer(t)
		l, q := newLoader(t, s, Options{})
		u := s.URL + "/shared.png"
		s.mu.Lock()
		s.gate = make(chan struct{})
		s.mu.Unlock()
		// Two sizes of a URL that SizedURL leaves unchanged.
		l.Get(u, 96)
		l.Get(u, 576)
		synctest.Wait()
		close(s.gate)
		q.wait(t)
		waitIdle(t, l)
		if l.Get(u, 96) == nil || l.Get(u, 576) == nil {
			t.Fatal("not both loaded")
		}
		if n := s.count("/shared.png"); n != 1 {
			t.Errorf("fetched %d times", n)
		}
		if a, b := l.Get(u, 96).RGBA.Bounds().Dx(), l.Get(u, 576).RGBA.Bounds().Dx(); a != 96 || b != 576 {
			t.Errorf("sizes %d and %d", a, b)
		}
	})
}

func TestPostsCoalesce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newServer(t)
		l, q := newLoader(t, s, Options{})
		for i := range 6 {
			l.Get(s.URL+"/c"+string(rune('a'+i))+".png", 96)
		}
		waitIdle(t, l)
		// One post is pending for all six until the UI runs it.
		if len(q) != 1 {
			t.Fatalf("%d posts pending", len(q))
		}
		q.wait(t)
		for i := range 6 {
			if l.Get(s.URL+"/c"+string(rune('a'+i))+".png", 96) == nil {
				t.Errorf("image %d not loaded", i)
			}
		}
		// Once it ran, the next load posts again.
		l.Get(s.URL+"/later.png", 96)
		q.wait(t)
	})
}

func TestFailureIsCachedForAWhile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newServer(t)
		l, q := newLoader(t, s, Options{FailFor: time.Minute})
		u := s.URL + "/missing.png"
		l.Get(u, 96)
		waitIdle(t, l)
		// A failure changes nothing on screen, so it posts nothing.
		q.none(t)
		for range 100 {
			if l.Get(u, 96) != nil {
				t.Fatal("got a missing image")
			}
		}
		waitIdle(t, l)
		if n := s.count("/missing.png"); n != 1 {
			t.Fatalf("fetched %d times within a minute", n)
		}
		time.Sleep(61 * time.Second)
		l.Get(u, 96)
		waitIdle(t, l)
		if n := s.count("/missing.png"); n != 2 {
			t.Errorf("fetched %d times after a minute, want 2", n)
		}
	})
}

func TestFailuresAreBounded(t *testing.T) {
	l := offlineLoader(t, 0)
	failed := errors.New("artwork: offline")
	for i := range 3 * maxFailed {
		l.finish(key{coverURL(i), 96}, nil, failed)
	}
	l.mu.Lock()
	n := len(l.failed)
	l.mu.Unlock()
	if n == 0 || n > maxFailed {
		t.Errorf("%d failures kept, want 1 to %d", n, maxFailed)
	}
}

func TestMemoryBudgetEvictsLeastRecentlyUsed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newServer(t)
		const each = 96 * 96 * 4
		l, q := newLoader(t, s, Options{MemoryBytes: 3 * each})
		get := func(name string) *Image { return l.Get(s.URL+"/"+name+".png", 96) }
		load := func(name string) {
			t.Helper()
			get(name)
			q.wait(t)
			waitIdle(t, l)
			if get(name) == nil {
				t.Fatalf("%s not loaded", name)
			}
		}
		for _, name := range []string{"a", "b", "c"} {
			l.NextFrame()
			load(name)
		}
		// Frames go by; a is used again, so b is the least recent.
		l.NextFrame()
		l.NextFrame()
		get("a")
		l.NextFrame()
		l.NextFrame()
		load("d")
		l.mu.Lock()
		_, hasB := l.mem[key{s.URL + "/b.png", 96}]
		used, count := l.bytes, len(l.mem)
		l.mu.Unlock()
		if hasB || count != 3 || used != 3*each {
			t.Errorf("b kept %v, %d images in %d bytes", hasB, count, used)
		}
		if get("a") == nil || get("c") == nil {
			t.Error("evicted a more recent image")
		}
		// b is fetched again, as it is not on disk.
		load("b")
		if fetches := s.count("/b.png"); fetches != 2 {
			t.Errorf("b fetched %d times", fetches)
		}
	})
}

func TestMemoryBudgetKeepsWhatIsOnScreen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newServer(t)
		const each = 96 * 96 * 4
		l, q := newLoader(t, s, Options{MemoryBytes: 2 * each})
		names := []string{"a", "b", "c", "d"}
		// One frame shows four covers, over the budget of two.
		l.NextFrame()
		for _, name := range names {
			l.Get(s.URL+"/"+name+".png", 96)
		}
		waitIdle(t, l)
		q.wait(t)
		l.NextFrame()
		for _, name := range names {
			if l.Get(s.URL+"/"+name+".png", 96) == nil {
				t.Fatalf("%s evicted while on screen", name)
			}
		}
		// Once the page changes, the next load evicts down to the budget.
		l.NextFrame()
		l.NextFrame()
		l.Get(s.URL+"/e.png", 96)
		q.wait(t)
		waitIdle(t, l)
		l.mu.Lock()
		kept := len(l.mem)
		l.mu.Unlock()
		if kept != 2 {
			t.Errorf("%d images kept, want 2", kept)
		}
	})
}

func TestMemoryBudgetEvictsWhatAPageLeftShowed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newServer(t)
		const each = 96 * 96 * 4
		l, q := newLoader(t, s, Options{MemoryBytes: 2 * each})
		shown := func(names ...string) {
			l.NextFrame()
			for _, name := range names {
				l.Get(s.URL+"/"+name+".png", 96)
			}
		}
		// A page of three, over budget, then a page of a, already loaded.
		shown("a", "b", "c")
		waitIdle(t, l)
		q.wait(t)
		shown("a", "b", "c")
		shown("a")
		shown("a")
		l.mu.Lock()
		count, used := len(l.mem), l.bytes
		l.mu.Unlock()
		if count != 2 || used != 2*each {
			t.Errorf("%d images in %d bytes kept with nothing loading, want 2", count, used)
		}
		if l.Get(s.URL+"/a.png", 96) == nil {
			t.Error("evicted the image on screen")
		}
	})
}

func TestDiskCache(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newServer(t)
		dir := t.TempDir()
		u := s.URL + "/disk.png"
		l, q := newLoader(t, s, Options{Dir: dir})
		l.Get(u, 96)
		q.wait(t)
		waitIdle(t, l)
		// Another run reads it from disk.
		nextRun, nextPosts := newLoader(t, s, Options{Dir: dir})
		nextRun.Get(u, 96)
		nextPosts.wait(t)
		if nextRun.Get(u, 96) == nil {
			t.Fatal("not loaded from disk")
		}
		if n := s.count("/disk.png"); n != 1 {
			t.Errorf("fetched %d times", n)
		}
		// A corrupt file is dropped and fetched again.
		d := &disk{dir: dir}
		if err := os.WriteFile(d.path(u), []byte("garbage"), 0o644); err != nil {
			t.Fatal(err)
		}
		corrupt, corruptPosts := newLoader(t, s, Options{Dir: dir})
		corrupt.Get(u, 96)
		waitIdle(t, corrupt)
		time.Sleep(defaultFailFor)
		corrupt.Get(u, 96)
		corruptPosts.wait(t)
		if corrupt.Get(u, 96) == nil || s.count("/disk.png") != 2 {
			t.Errorf("corrupt file: fetched %d times", s.count("/disk.png"))
		}
	})
}

func TestDiskCachePrunesOldest(t *testing.T) {
	dir := t.TempDir()
	d := &disk{dir: dir, maxBytes: 1000}
	data := make([]byte, 300)
	old := time.Now().Add(-time.Hour)
	for i, u := range []string{"a", "b", "c"} {
		d.put(u, data)
		// a is the oldest, then b, then c.
		at := old.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(d.path(u), at, at); err != nil {
			t.Fatal(err)
		}
	}
	// Reading b makes it recent.
	if _, ok := d.get("b"); !ok {
		t.Fatal("b not cached")
	}
	d.put("d", data) // 1200 bytes: prunes to 750
	for u, want := range map[string]bool{"a": false, "b": true, "c": false, "d": true} {
		if _, err := os.Stat(d.path(u)); (err == nil) != want {
			t.Errorf("%s kept %v, want %v", u, err == nil, want)
		}
	}
	if d.size != 600 {
		t.Errorf("size %d, want 600", d.size)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*"))
	if len(files) != 2 {
		t.Errorf("files %q", files)
	}
}

// A crash leaves a temporary file, which the first write's scan removes
// once it is too old to be another process's write in progress.
func TestDiskCacheRemovesLeftovers(t *testing.T) {
	dir := t.TempDir()
	old, fresh := filepath.Join(dir, "tmp-old"), filepath.Join(dir, "tmp-fresh")
	for _, path := range []string{old, fresh} {
		if err := os.WriteFile(path, []byte{1}, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	long := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, long, long); err != nil {
		t.Fatal(err)
	}
	d := &disk{dir: dir, maxBytes: 1000}
	d.put("a", []byte{1, 2, 3})
	if _, err := os.Stat(old); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the old leftover stays: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("a write in progress went: %v", err)
	}
}

func TestQueueFetchesNewestFirstAndForgetsTheOldest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newServer(t)
		s.mu.Lock()
		s.gate = make(chan struct{})
		// Small, so one worker decodes them all quickly.
		s.png = encodePNG(t, solid(8, 8, color.RGBA{255, 0, 0, 255}, color.RGBA{}))
		s.mu.Unlock()
		l, q := newLoader(t, s, Options{Workers: 1})
		url := func(i int) string { return s.URL + "/q" + string(rune('A'+i/26)) + string(rune('a'+i%26)) + ".png" }
		// The worker blocks on the first while the rest overflow the queue,
		// as when a list scrolls fast.
		l.Get(url(0), 96)
		synctest.Wait()
		for i := 1; i <= maxQueue+10; i++ {
			l.Get(url(i), 96)
		}
		l.mu.Lock()
		queued, oldest := len(l.queue), l.loading[key{url(1), 96}]
		l.mu.Unlock()
		if queued != maxQueue || oldest {
			t.Fatalf("%d queued, oldest still loading %v", queued, oldest)
		}
		close(s.gate)
		q.wait(t)
		waitIdle(t, l)
		if s.count("/qAb.png") != 0 {
			t.Error("fetched a forgotten request")
		}
		if l.Get(url(maxQueue+10), 96) == nil {
			t.Error("newest not loaded")
		}
	})
}

func TestCloseCancels(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newServer(t)
		s.mu.Lock()
		s.gate = make(chan struct{})
		s.mu.Unlock()
		defer close(s.gate)
		l, q := newLoader(t, s, Options{})
		l.Get(s.URL+"/slow.png", 96)
		synctest.Wait()
		var done atomic.Bool
		go func() { l.Close(); done.Store(true) }()
		waitIdle(t, l)
		q.none(t)
		if l.Get(s.URL+"/other.png", 96) != nil || s.count("/other.png") != 0 {
			t.Error("loaded after Close")
		}
		l.mu.Lock()
		failed := len(l.failed)
		l.mu.Unlock()
		if failed != 0 || !done.Load() {
			t.Errorf("canceled load cached as failed (%d), or Close blocked", failed)
		}
	})
}

func TestOtherSizeShowsWhileLoading(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newServer(t)
		l, q := newLoader(t, s, Options{})
		u := s.URL + "/sizes.png"
		for _, px := range []int{96, 384} {
			l.Get(u, px)
			q.wait(t)
			waitIdle(t, l)
		}
		s.mu.Lock()
		s.gate = make(chan struct{})
		s.mu.Unlock()
		defer close(s.gate)
		// While a size loads: the smallest that covers it, else the largest.
		for _, tc := range []struct{ px, got int }{{128, 384}, {544, 384}, {64, 96}, {256, 384}} {
			img := l.Get(u, tc.px)
			if img == nil || img.RGBA.Bounds().Dx() != tc.got {
				t.Errorf("Get(%d) while loading: %v, want the %d image", tc.px, img, tc.got)
			}
		}
		if l.Get(s.URL+"/unseen.png", 96) != nil {
			t.Error("an image of another URL")
		}
	})
}
