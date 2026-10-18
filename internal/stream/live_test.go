package stream

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var live = flag.Bool("live", false,
	"resolve real songs and download them: go test -run Live -live -v ./internal/stream/")

// webmMagic starts an EBML file, as WebM is.
const webmMagic = "\x1a\x45\xdf\xa3"

var liveSongs = []struct {
	name, id string
	playable bool
	// needsYTDLP: only yt-dlp, with a JS runtime, plays it.
	needsYTDLP bool
}{
	{"popular official", "kJQP7kiw5Fk", true, false}, // Luis Fonsi - Despacito
	{"album track", "swJOIjjW69U", true, false},      // Fleetwood Mac - Dreams, from Rumours
	{"older upload", "dQw4w9WgXcQ", true, false},     // 2009
	{"made for kids", "XqZsoesa55w", true, true},
	{"age-gated", "07FYdnEawAQ", false, false},
	{"invalid", "aaaaaaaaaaa", false, false},
}

// TestLive is the only test touching the network. Beyond resolving, it
// proves each URL plays: the head is WebM, the tail is served (a URL
// missing a PO token is cut off after a MiB), and the download is not
// throttled (an unsolved n challenge caps it at tens of KB/s). VISIONOS and
// yt-dlp are also measured alone; only the default path must pass.
func TestLive(t *testing.T) {
	if !*live {
		t.Skip("touches YouTube; run with -live")
	}
	calls := &countingTransport{next: http.DefaultTransport}
	hc := &http.Client{Timeout: time.Minute, Transport: calls}
	paths := []struct {
		name    string
		y       *YouTube
		resolve func(*YouTube, context.Context, string) (Stream, error)
		must    bool
	}{
		{"default", New(Options{HTTP: hc, YTDLP: "yt-dlp"}), (*YouTube).Resolve, true},
		{"VISIONOS", New(Options{HTTP: hc, NoProbe: true}), (*YouTube).Resolve, false},
		{"yt-dlp", New(Options{HTTP: hc, YTDLP: "yt-dlp", NoProbe: true}), (*YouTube).viaYTDLP, false},
	}
	_, errYTDLP := lookPath("yt-dlp", searchPath())
	for _, p := range paths {
		for _, song := range liveSongs {
			t.Run(p.name+"/"+song.name, func(t *testing.T) {
				if errYTDLP != nil && (p.name == "yt-dlp" || p.must && song.needsYTDLP) {
					t.Skip(errYTDLP)
				}
				calls.reset()
				start := time.Now()
				s, err := p.resolve(p.y, t.Context(), song.id)
				took := time.Since(start).Round(time.Millisecond)
				t.Logf("%s %s: %d player requests, %d probes", p.name, song.id, calls.player.Load(), calls.other.Load())
				if !song.playable {
					t.Logf("%s %s: resolve %v: %v", p.name, song.id, took, err)
					if p.must && !errors.Is(err, ErrUnplayable) {
						t.Errorf("err = %v, want ErrUnplayable", err)
					}
					return
				}
				if err != nil {
					t.Logf("%s %s: resolve %v: %v", p.name, song.id, took, err)
					if p.must {
						t.Fatal(err)
					}
					return
				}
				r := measure(t.Context(), hc, s)
				t.Logf("%s %s: resolve %v; %s %d kb/s %d B %.0fs, expires in %v; head %d %q, tail %d, %s",
					p.name, song.id, took, s.MimeType, s.Bitrate/1000, s.Size, s.Seconds,
					time.Until(s.Expires).Round(time.Minute), r.headStatus, r.magic, r.tailStatus, r.speed)
				if p.must {
					if r.headStatus != http.StatusPartialContent || r.magic != webmMagic || r.tailStatus != http.StatusPartialContent {
						t.Errorf("URL does not play: %+v", r)
					}
					if r.mbps < 1 {
						t.Errorf("throttled: %s", r.speed)
					}
				}
			})
		}
	}
}

// countingTransport counts player and googlevideo requests apart, to show
// the visitorData retry and the probe.
type countingTransport struct {
	next          http.RoundTripper
	player, other atomic.Int32
}

func (c *countingTransport) reset() {
	c.player.Store(0)
	c.other.Store(0)
}

func (c *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasSuffix(req.URL.Path, "/player") {
		c.player.Add(1)
	} else {
		c.other.Add(1)
	}
	return c.next.RoundTrip(req)
}

type liveResult struct {
	headStatus, tailStatus int
	magic                  string
	mbps                   float64
	speed                  string
}

func measure(ctx context.Context, hc *http.Client, s Stream) liveResult {
	var r liveResult
	head, status, _ := rangeGet(ctx, hc, s, 0, 256<<10-1)
	r.headStatus = status
	if len(head) >= 4 {
		r.magic = string(head[:4])
	}
	if s.Size > 0 {
		_, r.tailStatus, _ = rangeGet(ctx, hc, s, max(s.Size-64<<10, 0), s.Size-1)
	}
	size := int64(4 << 20)
	if s.Size > 0 {
		size = min(size, s.Size)
	}
	start := time.Now()
	body, status, err := rangeGet(ctx, hc, s, 0, size-1)
	sec := time.Since(start).Seconds()
	if err != nil || status != http.StatusPartialContent {
		r.speed = fmt.Sprintf("download HTTP %d after %d B: %v", status, len(body), err)
		return r
	}
	r.mbps = float64(len(body)) / sec / 1e6
	r.speed = fmt.Sprintf("%.1f MB in %.2fs = %.1f MB/s", float64(len(body))/1e6, sec, r.mbps)
	return r
}

func rangeGet(ctx context.Context, hc *http.Client, s Stream, from, to int64) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return nil, 0, err
	}
	maps.Copy(req.Header, s.Header)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", from, to))
	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	var b bytes.Buffer
	_, err = io.Copy(&b, resp.Body)
	return b.Bytes(), resp.StatusCode, err
}
