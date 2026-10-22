// Package artwork fetches cover images, decodes them at the size they are
// shown, and caches them in memory and on disk. It knows nothing of the UI:
// finished loads reach it through Options.Post, which runs on the UI thread.
package artwork

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"
	"yt-music-player/internal/httpclient"
)

const (
	// defaultMemoryBytes holds about two screens of the largest covers
	// (a 168 DIP card at 2× is 352 px, 484 KB), so going back to a page
	// shows it at once.
	defaultMemoryBytes = 32 << 20
	defaultDiskBytes   = 128 << 20
	// defaultWorkers fills a page quickly without competing with the
	// music API.
	defaultWorkers = 4
	defaultFailFor = time.Minute

	// maxQueue bounds pending loads, as scrolling a long list asks for
	// every cover it passes. The oldest are dropped; scrolling back asks
	// again.
	maxQueue  = 128
	maxBody   = 16 << 20
	maxFailed = 1 << 12
)

// Options configure a Loader.
type Options struct {
	// Post runs fn on the UI thread and redraws. Loads that finish before
	// the UI ran the last post share one. Nil posts nothing.
	Post func(fn func())
	// Prepare converts decoded pixels into the toolkit's bitmap, off the
	// UI thread; the result is Image.UI.
	Prepare func(*image.RGBA) any
	// Dir is the disk cache's directory, empty for none.
	Dir string
	// MemoryBytes, Workers and FailFor take defaults when zero.
	MemoryBytes int
	Workers     int
	FailFor     time.Duration
	// Client defaults to httpclient's, with a timeout.
	Client *http.Client
}

// key is an image at a size: one URL shown at two sizes is two images.
type key struct {
	url string
	px  int
}

// entry is an image in memory, in a list from most to least recently used.
type entry struct {
	key        key
	img        *Image
	prev, next *entry
	shownFrame uint64
}

// download is a fetch in flight; other keys of the same URL wait for it.
type download struct {
	done chan struct{}
	data []byte
	err  error
}

// Loader loads images off the UI thread and keeps them. Its methods are
// safe for concurrent use; Get is meant for the UI thread's views.
type Loader struct {
	opts   Options
	disk   *disk
	ctx    context.Context
	cancel context.CancelFunc

	mu sync.Mutex
	// mem indexes the list from lru.next (most recent) to lru.prev
	// (least); bytes is what their pixels take.
	mem   map[key]*entry
	lru   entry
	bytes int
	// sizes are the sizes in mem of each URL.
	sizes map[string][]int
	// frame counts NextFrame calls.
	frame     uint64
	loading   map[key]bool
	queue     []key
	workers   int
	failed    map[key]time.Time
	averages  map[string]color.RGBA
	downloads map[string]*download
	isClosed  bool

	postPending atomic.Bool
}

// New makes a Loader, with defaults for opts' zero fields. Close stops it.
func New(opts Options) *Loader {
	if opts.MemoryBytes <= 0 {
		opts.MemoryBytes = defaultMemoryBytes
	}
	if opts.Workers <= 0 {
		opts.Workers = defaultWorkers
	}
	if opts.FailFor <= 0 {
		opts.FailFor = defaultFailFor
	}
	if opts.Client == nil {
		opts.Client = httpclient.New(30 * time.Second)
	}
	l := &Loader{
		opts:      opts,
		mem:       map[key]*entry{},
		sizes:     map[string][]int{},
		loading:   map[key]bool{},
		failed:    map[key]time.Time{},
		averages:  map[string]color.RGBA{},
		downloads: map[string]*download{},
	}
	l.lru.next, l.lru.prev = &l.lru, &l.lru
	if opts.Dir != "" {
		l.disk = &disk{dir: opts.Dir, maxBytes: defaultDiskBytes}
	}
	l.ctx, l.cancel = context.WithCancel(context.Background())
	return l
}

// Get returns the image at url for a cover px pixels wide (see Bucket). A
// miss starts loading it once, and Options.Post tells when it is done.
// Until then, and for a while after a failure, Get returns the image at
// another size if memory has one (as after a resize past a bucket), else nil.
func (l *Loader) Get(url string, px int) *Image {
	k := key{url, Bucket(px)}
	l.mu.Lock()
	defer l.mu.Unlock()
	if e := l.mem[k]; e != nil {
		l.use(e)
		return e.img
	}
	l.request(k)
	best := 0
	for _, size := range l.sizes[url] {
		if best == 0 || better(size, best, k.px) {
			best = size
		}
	}
	if best == 0 {
		return nil
	}
	e := l.mem[key{url, best}]
	l.use(e)
	return e.img
}

// better reports whether size a suits a cover of want pixels better than
// size b: the smallest at least as large, which the GPU scales down
// sharply, else the largest.
func better(a, b, want int) bool {
	if (a >= want) != (b >= want) {
		return a >= want
	}
	if a >= want {
		return a < b
	}
	return a > b
}

func (l *Loader) use(e *entry) {
	e.shownFrame = l.frame
	l.unlink(e)
	l.pushFront(e)
}

// request queues k unless it is loading or failed recently. Needs l.mu.
func (l *Loader) request(k key) {
	if l.isClosed || l.loading[k] {
		return
	}
	if until, ok := l.failed[k]; ok {
		if time.Now().Before(until) {
			return
		}
		delete(l.failed, k)
	}
	l.loading[k] = true
	l.queue = append(l.queue, k)
	if len(l.queue) > maxQueue {
		delete(l.loading, l.queue[0])
		l.queue = l.queue[1:]
	}
	if l.workers < l.opts.Workers {
		l.workers++
		go l.work()
	}
}

// Average returns the average color of the image at url, loaded at any
// size: a page's glow wants the cover's color whatever size it shows.
func (l *Loader) Average(url string) (color.RGBA, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok := l.averages[url]
	return c, ok
}

// NextFrame starts a frame. Images got in this frame or the previous one
// are on screen and never evicted: the UI holds them anyway and would only
// load them again. Images of a page left are evicted once over budget.
func (l *Loader) NextFrame() {
	l.mu.Lock()
	l.frame++
	if l.bytes > l.opts.MemoryBytes {
		l.evict()
	}
	l.mu.Unlock()
}

// Close cancels loads in flight and starts no others; later Gets return
// only what memory holds.
func (l *Loader) Close() {
	l.mu.Lock()
	l.isClosed = true
	l.queue = nil
	l.mu.Unlock()
	l.cancel()
}

// work loads queued images, newest first.
func (l *Loader) work() {
	for {
		l.mu.Lock()
		n := len(l.queue)
		if n == 0 || l.isClosed {
			l.workers--
			l.mu.Unlock()
			return
		}
		k := l.queue[n-1]
		l.queue = l.queue[:n-1]
		l.mu.Unlock()
		img, err := l.load(k)
		l.finish(k, img, err)
	}
}

func (l *Loader) load(k key) (*Image, error) {
	u := SizedURL(k.url, k.px)
	data, err := l.fetch(u)
	if err != nil {
		return nil, err
	}
	img, err := Decode(data, k.px)
	if err != nil {
		// Or the next try would read the same bytes.
		l.disk.remove(u)
		return nil, err
	}
	if l.opts.Prepare != nil {
		img.UI = l.opts.Prepare(img.RGBA)
	}
	return img, nil
}

func (l *Loader) finish(k key, img *Image, err error) {
	l.mu.Lock()
	delete(l.loading, k)
	switch {
	case l.isClosed || errors.Is(err, context.Canceled):
	case err != nil:
		// Bounds the failures of a long session, kept until asked again:
		// those cleared are only tried again sooner.
		if len(l.failed) >= maxFailed {
			clear(l.failed)
		}
		l.failed[k] = time.Now().Add(l.opts.FailFor)
	default:
		e := &entry{key: k, img: img, shownFrame: l.frame}
		l.mem[k] = e
		l.sizes[k.url] = append(l.sizes[k.url], k.px)
		l.pushFront(e)
		l.bytes += img.Bytes()
		// Bounds the colors of every URL seen in a long session.
		if len(l.averages) >= 1<<14 {
			clear(l.averages)
		}
		l.averages[k.url] = img.Average
		l.evict()
	}
	// Failures show nothing new, so post nothing. One post shows every
	// load finished before it runs; claiming it under the lock keeps
	// IsBusy true until then.
	canPost := err == nil && !l.isClosed && l.opts.Post != nil
	post := canPost && l.postPending.CompareAndSwap(false, true)
	l.mu.Unlock()
	if post {
		l.opts.Post(func() { l.postPending.Store(false) })
	}
}

// IsBusy reports whether an image is loading, or loaded and not yet
// posted, for tests to wait on.
func (l *Loader) IsBusy() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.loading) > 0 || l.postPending.Load()
}

// evict drops the least recently used images over budget, except those on
// screen. Needs l.mu.
func (l *Loader) evict() {
	for l.bytes > l.opts.MemoryBytes {
		e := l.lru.prev
		// The list is in order of use, so the rest are on screen too.
		if e == &l.lru || e.shownFrame+1 >= l.frame {
			return
		}
		l.unlink(e)
		delete(l.mem, e.key)
		l.bytes -= e.img.Bytes()
		sizes := slices.DeleteFunc(l.sizes[e.key.url], func(px int) bool { return px == e.key.px })
		if len(sizes) == 0 {
			delete(l.sizes, e.key.url)
		} else {
			l.sizes[e.key.url] = sizes
		}
	}
}

func (l *Loader) pushFront(e *entry) {
	e.prev, e.next = &l.lru, l.lru.next
	l.lru.next.prev = e
	l.lru.next = e
}

func (l *Loader) unlink(e *entry) {
	e.prev.next, e.next.prev = e.next, e.prev
	e.prev, e.next = nil, nil
}

// fetch reads u from the disk cache or downloads it, once for all keys
// sharing the URL.
func (l *Loader) fetch(u string) ([]byte, error) {
	if data, ok := l.disk.get(u); ok {
		return data, nil
	}
	l.mu.Lock()
	if d := l.downloads[u]; d != nil {
		l.mu.Unlock()
		select {
		case <-d.done:
			return d.data, d.err
		case <-l.ctx.Done():
			return nil, l.ctx.Err()
		}
	}
	d := &download{done: make(chan struct{})}
	l.downloads[u] = d
	l.mu.Unlock()
	d.data, d.err = l.download(u)
	if d.err == nil {
		l.disk.put(u, d.data)
	}
	l.mu.Lock()
	delete(l.downloads, u)
	l.mu.Unlock()
	close(d.done)
	return d.data, d.err
}

func (l *Loader) download(u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(l.ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := l.opts.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("artwork: %s: %s", u, resp.Status)
	}
	switch n := resp.ContentLength; {
	case n > maxBody:
		return nil, fmt.Errorf("artwork: %s: over %d bytes", u, maxBody)
	case n >= 0:
		// Image hosts send the length: one buffer of it saves io.ReadAll's
		// 22 growth allocations. net/http fails a body shorter than it.
		data := make([]byte, n)
		if _, err := io.ReadFull(resp.Body, data); err != nil {
			return nil, err
		}
		return data, nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBody {
		return nil, fmt.Errorf("artwork: %s: over %d bytes", u, maxBody)
	}
	return data, nil
}
