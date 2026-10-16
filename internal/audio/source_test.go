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

func serveBytes(t *testing.T, data []byte) *rangeServer {
	t.Helper()
	s := &rangeServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		s.mu.Lock()
		s.ranges = append(s.ranges, r.Header.Get("Range"))
		s.headers = append(s.headers, r.Header.Clone())
		s.mu.Unlock()
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *rangeServer) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.ranges...)
}

func newTestSource(t *testing.T, url string, onWait func(bool)) *httpSource {
	t.Helper()
	src := newHTTPSource(http.DefaultClient, url, http.Header{"User-Agent": {"test-agent"}}, onWait)
	t.Cleanup(func() { _ = src.Close() })
	return src
}

func TestSourceReadsInRanges(t *testing.T) {
	data := testBytes(5<<20 + 1234)
	srv := serveBytes(t, data)
	src := newTestSource(t, srv.URL, nil)
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
}

func TestSourceSeeks(t *testing.T) {
	data := testBytes(12 << 20)
	srv := serveBytes(t, data)
	src := newTestSource(t, srv.URL, nil)
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
	src.mu.Lock()
	kept := src.kept
	src.mu.Unlock()
	if kept > keepBytes+readAhead {
		t.Errorf("keeps %d bytes", kept)
	}
}

func TestSourceTellsWaits(t *testing.T) {
	data := testBytes(1 << 20)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
	}))
	t.Cleanup(srv.Close)
	var mu sync.Mutex
	var waits []bool
	src := newTestSource(t, srv.URL, func(w bool) {
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
}

func TestSourceFails(t *testing.T) {
	retryWait = time.Millisecond
	t.Cleanup(func() { retryWait = 500 * time.Millisecond })
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
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(tc.status)
			}))
			t.Cleanup(srv.Close)
			src := newTestSource(t, srv.URL+"/videoplayback?sig=secret", nil)
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
	}
}

func TestSourceRetries(t *testing.T) {
	retryWait = time.Millisecond
	t.Cleanup(func() { retryWait = 500 * time.Millisecond })
	data := testBytes(300 << 10)
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) <= maxAttempts-1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
	}))
	t.Cleanup(srv.Close)
	got, err := io.ReadAll(newTestSource(t, srv.URL, nil))
	if err != nil || !bytes.Equal(got, data) {
		t.Errorf("read %d bytes, %v", len(got), err)
	}
}

func TestSourceCloseEndsAWaitingRead(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-block }))
	t.Cleanup(func() {
		close(block)
		srv.Close()
	})
	src := newTestSource(t, srv.URL, nil)
	done := make(chan error)
	go func() {
		_, err := src.Read(make([]byte, 10))
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	_ = src.Close()
	select {
	case err := <-done:
		if !errors.Is(err, errClosed) {
			t.Errorf("err %v, want errClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the read still waits")
	}
}
