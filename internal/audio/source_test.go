package audio

import (
	"bytes"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// testBytes returns n deterministic bytes.
func testBytes(n int) []byte {
	b := make([]byte, n)
	r := rand.New(rand.NewPCG(1, 2))
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}

// rangeServer serves data with range requests, as googlevideo does, and
// counts the requests.
type rangeServer struct {
	*httptest.Server
	requests atomic.Int32
	mu       sync.Mutex
	ranges   []string
	headers  []http.Header
}

// startServer serves h on an in-memory network, which a synctest bubble
// can wait on; its Client is the only way there.
func startServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewTestServer(t, h)
	// Starts it, setting its URL.
	srv.Client()
	return srv
}

func serveBytes(t *testing.T, data []byte) *rangeServer {
	t.Helper()
	s := &rangeServer{}
	s.Server = startServer(t, func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		s.mu.Lock()
		s.ranges = append(s.ranges, r.Header.Get("Range"))
		s.headers = append(s.headers, r.Header.Clone())
		s.mu.Unlock()
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
	})
	return s
}

func (s *rangeServer) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.ranges...)
}

func newTestSource(t *testing.T, srv *httptest.Server, path string, onWait func(bool)) *httpSource {
	t.Helper()
	src := newHTTPSource(srv.Client(), srv.URL+path, http.Header{"User-Agent": {"test-agent"}}, onWait)
	t.Cleanup(func() { _ = src.Close() })
	return src
}

func TestSourceReadsInRanges(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		data := testBytes(5<<20 + 1234)
		srv := serveBytes(t, data)
		src := newTestSource(t, srv.Server, "", nil)
		got, err := io.ReadAll(src)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("read %d bytes, not the %d served", len(got), len(data))
		}
		for _, r := range srv.seen() {
			if !strings.HasPrefix(r, "bytes=") {
				t.Errorf("a request without a range: %q", r)
			}
		}
		if n := len(srv.seen()); n < 3 {
			t.Errorf("%d requests for 5 MiB: not read ahead in ranges", n)
		}
		srv.mu.Lock()
		ua := srv.headers[0].Get("User-Agent")
		srv.mu.Unlock()
		if ua != "test-agent" {
			t.Errorf("User-Agent %q, want the Media's", ua)
		}
	})
}

func TestSourceSeeks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		data := testBytes(12 << 20)
		srv := serveBytes(t, data)
		src := newTestSource(t, srv.Server, "", nil)
		buf := make([]byte, 1000)
		for _, at := range []int64{0, 9 << 20, 100, 11<<20 + 77, 3 << 20, 9<<20 + 5} {
			if _, err := src.Seek(at, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			if _, err := io.ReadFull(src, buf); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(buf, data[at:at+1000]) {
				t.Fatalf("at %d, other bytes", at)
			}
		}
		if end, err := src.Seek(-10, io.SeekEnd); err != nil || end != int64(len(data))-10 {
			t.Errorf("Seek from the end: %d, %v", end, err)
		}
		if n := src.buffers(); n > maxChunks {
			t.Errorf("%d chunks made, want at most %d", n, maxChunks)
		}
	})
}

func TestSourceSeekRejectsUnknownWhence(t *testing.T) {
	s := &httpSource{size: 100, pos: 10}
	if at, err := s.Seek(5, 3); err == nil || s.pos != 10 {
		t.Errorf("Seek(5, 3) = %d, %v; at %d, want an error and 10", at, err, s.pos)
	}
}

// maxChunks is how many chunks the window spans at most.
const maxChunks = (keepBehind+readAhead)/chunkSize + 2

// buffers counts the chunks the source made: kept, and dropped for reuse.
func (s *httpSource) buffers() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.chunks) + len(s.free)
}

// A song streams through a window of chunks, made once: its memory does
// not grow with its length.
func TestSourceKeepsAWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		data := testBytes(6 << 20)
		srv := serveBytes(t, data)
		src := newTestSource(t, srv.Server, "", nil)
		buf := make([]byte, 16<<10)
		for read := 0; read < len(data); {
			n, err := src.Read(buf)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(buf[:n], data[read:read+n]) {
				t.Fatalf("at %d, other bytes", read)
			}
			read += n
			if n := src.buffers(); n > maxChunks {
				t.Fatalf("at %d, %d chunks made, want at most %d", read, n, maxChunks)
			}
		}
		// Ranges of half the read-ahead to all of it: a reader as fast as
		// this one waits for each, which then asks for the whole window.
		if n := len(srv.seen()); n < len(data)/readAhead || n > 2*len(data)/readAhead {
			t.Errorf("%d requests for %d bytes", n, len(data))
		}
	})
}

func TestSourceTellsWaits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		data := testBytes(1 << 20)
		release := make(chan struct{})
		srv := startServer(t, func(w http.ResponseWriter, r *http.Request) {
			<-release
			http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
		})
		var mu sync.Mutex
		var waits []bool
		src := newTestSource(t, srv, "", func(w bool) {
			mu.Lock()
			waits = append(waits, w)
			mu.Unlock()
		})
		time.AfterFunc(50*time.Millisecond, func() { close(release) })
		if _, err := io.ReadFull(src, make([]byte, 100)); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(waits) != 2 || !waits[0] || waits[1] {
			t.Errorf("waits %v, want [true false]", waits)
		}
	})
}

func TestSourceFails(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		requests int32
	}{
		{"expired", http.StatusForbidden, 1},
		{"gone", http.StatusGone, 1},
		{"server error", http.StatusServiceUnavailable, maxAttempts},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var requests atomic.Int32
				srv := startServer(t, func(w http.ResponseWriter, _ *http.Request) {
					requests.Add(1)
					w.WriteHeader(tc.status)
				})
				src := newTestSource(t, srv, "/videoplayback?sig=secret", nil)
				_, err := src.Read(make([]byte, 10))
				if status, ok := errors.AsType[statusError](err); !ok || int(status) != tc.status {
					t.Errorf("err %v, want HTTP %d", err, tc.status)
				}
				if strings.Contains(err.Error(), "secret") {
					t.Errorf("the error shows the URL: %v", err)
				}
				if got := requests.Load(); got != tc.requests {
					t.Errorf("%d requests, want %d", got, tc.requests)
				}
			})
		})
	}
}

func TestSourceRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		data := testBytes(300 << 10)
		var requests atomic.Int32
		srv := startServer(t, func(w http.ResponseWriter, r *http.Request) {
			if requests.Add(1) <= maxAttempts-1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
		})
		start := time.Now()
		got, err := io.ReadAll(newTestSource(t, srv, "", nil))
		if err != nil || !bytes.Equal(got, data) {
			t.Errorf("read %d bytes, %v", len(got), err)
		}
		// The bubble's clock: each failure waited twice the one before.
		if waited, want := time.Since(start), retryWait*(1<<(maxAttempts-1)-1); waited != want {
			t.Errorf("waited %v between attempts, want %v", waited, want)
		}
	})
}

func TestSourceCloseEndsAWaitingRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		block := make(chan struct{})
		srv := startServer(t, func(http.ResponseWriter, *http.Request) { <-block })
		t.Cleanup(func() { close(block) })
		src := newTestSource(t, srv, "", nil)
		done := make(chan error)
		go func() {
			_, err := src.Read(make([]byte, 10))
			done <- err
		}()
		// Until the read waits for the network.
		synctest.Wait()
		_ = src.Close()
		if err := <-done; !errors.Is(err, errClosed) {
			t.Errorf("err %v, want errClosed", err)
		}
	})
}
