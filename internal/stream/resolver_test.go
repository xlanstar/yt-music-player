package stream

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestResolve(t *testing.T) {
	f := newFake(t, fixed("player_ok.json"))
	y := f.resolver(Options{VisitorData: "SEED"})
	s, err := y.Resolve(t.Context(), okID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s.URL, fixtureHost+"/videoplayback?") || !strings.Contains(s.URL, "itag=251&") {
		t.Errorf("URL = %s, want itag 251", s.URL)
	}
	if want := `audio/webm; codecs="opus"`; s.MimeType != want {
		t.Errorf("MimeType = %s, want %s", s.MimeType, want)
	}
	if s.Bitrate != 134835 || s.Size != 5417353 || s.Seconds != 321.421 {
		t.Errorf("Bitrate, Size, Seconds = %d, %d, %v; want averageBitrate, contentLength, approxDurationMs",
			s.Bitrate, s.Size, s.Seconds)
	}
	if !s.Expires.Equal(okExpires) {
		t.Errorf("Expires = %v, want %v", s.Expires, okExpires)
	}
	if got := s.Header.Get("User-Agent"); got != visionOS.UserAgent {
		t.Errorf("Header User-Agent = %q, want VisionOS's", got)
	}

	reqs := f.players()
	if len(reqs) != 1 {
		t.Fatalf("player requests = %d, want 1", len(reqs))
	}
	r := reqs[0]
	if r.Client != "VISIONOS" || r.Version != visionOS.Version || r.ClientID != "101" ||
		r.UserAgent != visionOS.UserAgent || r.VideoID != okID {
		t.Errorf("request = %+v, want VisionOS asking for %s", r, okID)
	}
	if r.Visitor != "SEED" || r.VisitorHeader != "SEED" {
		t.Errorf("visitorData = %q, header %q; want the seed in both", r.Visitor, r.VisitorHeader)
	}
	if r.Body["contentCheckOk"] != true || r.Body["racyCheckOk"] != true {
		t.Errorf("body = %v, want content and racy checks passed", r.Body)
	}
	// The probe reads the end, past the MiB googlevideo always serves.
	if got, want := f.probeRanges(), []string{"bytes=5416329-5417352"}; !slices.Equal(got, want) {
		t.Errorf("probes = %q, want %q", got, want)
	}
}

func TestUnplayable(t *testing.T) {
	tests := []struct {
		name, fixture, status, reason string
	}{
		{"age", "player_login.json", "LOGIN_REQUIRED", "Sign in to confirm your age"},
		{"unplayable", "player_unplayable.json", "UNPLAYABLE", "Video unavailable: The page needs to be reloaded."},
		{"error", "player_error.json", "ERROR", "This video is unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake(t, fixed(tt.fixture))
			_, err := f.resolver(Options{}).Resolve(t.Context(), okID)
			if !errors.Is(err, ErrUnplayable) {
				t.Fatalf("err = %v, want ErrUnplayable", err)
			}
			pe, ok := errors.AsType[*PlayabilityError](err)
			if !ok {
				t.Fatalf("err = %v, want a PlayabilityError", err)
			}
			if pe.Client != "VISIONOS" || pe.Status != tt.status || pe.Reason != tt.reason {
				t.Errorf("got %+v, want %s %q", pe, tt.status, tt.reason)
			}
			if got := f.clients(); got != "VISIONOS()" {
				t.Errorf("asked %s, want VISIONOS once", got)
			}
			if msg := err.Error(); !strings.HasPrefix(msg, "stream: "+okID+": unplayable: VISIONOS: "+tt.status) {
				t.Errorf("err = %q, want the reason after the ID", err)
			}
		})
	}
}

func TestCipherOnlySkipped(t *testing.T) {
	f := newFake(t, fixed("player_cipher.json"))
	_, err := f.resolver(Options{}).Resolve(t.Context(), okID)
	if !errors.Is(err, ErrUnplayable) || !strings.Contains(err.Error(), "no direct Opus") {
		t.Fatalf("err = %v, want ErrUnplayable for no direct format", err)
	}
	if len(f.probeRanges()) != 0 {
		t.Error("probed a format without a URL")
	}
}

func TestInvalidID(t *testing.T) {
	f := newFake(t, fixed("player_ok.json"))
	y := f.resolver(Options{})
	for _, id := range []string{"", "short", "--exec=rm -rf", "FGBhQbmPwH8x", "FGBhQbmPw/8"} {
		if _, err := y.Resolve(t.Context(), id); !errors.Is(err, ErrUnplayable) {
			t.Errorf("Resolve(%q) = %v, want ErrUnplayable", id, err)
		}
	}
	if n := len(f.players()); n != 0 {
		t.Errorf("player requests = %d, want none", n)
	}
}

func TestVideoMismatch(t *testing.T) {
	f := newFake(t, fixed("player_ok.json"))
	_, err := f.resolver(Options{}).Resolve(t.Context(), "dQw4w9WgXcQ")
	if err == nil || errors.Is(err, ErrUnplayable) || !strings.Contains(err.Error(), "player response of "+okID) {
		t.Fatalf("err = %v, want the other video named, and not as unplayable", err)
	}
}

// botChecked bot-checks requests without a visitor session.
func botChecked(r playerReq) string {
	if r.Visitor == "" {
		return "player_bot.json"
	}
	return "player_ok.json"
}

// As the web app does, a bot-checked request is retried in the visitor
// session the check handed out, which is then kept.
func TestBotCheck(t *testing.T) {
	f := newFake(t, botChecked)
	y := f.resolver(Options{})
	for _, id := range []string{okID, okID} {
		y.Forget(id)
		if _, err := y.Resolve(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := y.Resolve(t.Context(), okID); err != nil {
		t.Fatal(err)
	}
	// Forget ends the session; the cached third Resolve asks nothing.
	want := "VISIONOS() VISIONOS(VISITOR_FRESH) VISIONOS() VISIONOS(VISITOR_FRESH)"
	if got := f.clients(); got != want {
		t.Errorf("asked %s, want %s", got, want)
	}
	for _, r := range f.players() {
		if r.Visitor != r.VisitorHeader {
			t.Errorf("visitorData %q but X-Goog-Visitor-Id %q", r.Visitor, r.VisitorHeader)
		}
	}
}

// YouTube enforces PO tokens per visitor session, so a URL refused past
// its first MiB means a new session.
func TestRefusedRotatesSession(t *testing.T) {
	f := newFake(t, botChecked)
	f.media = func(n int) int {
		if n == 1 {
			return 403
		}
		return 206
	}
	y := f.resolver(Options{})
	if _, err := y.Resolve(t.Context(), okID); err != nil {
		t.Fatal(err)
	}
	want := "VISIONOS() VISIONOS(VISITOR_FRESH) VISIONOS() VISIONOS(VISITOR_FRESH)"
	if got := f.clients(); got != want {
		t.Errorf("asked %s, want %s", got, want)
	}

	// Refused in every session: the client gets maxSessions.
	f = newFake(t, fixed("player_ok.json"))
	f.media = func(int) int { return 403 }
	_, err := f.resolver(Options{VisitorData: "V"}).Resolve(t.Context(), okID)
	if !errors.Is(err, errRefused) || errors.Is(err, ErrUnplayable) {
		t.Fatalf("err = %v, want refused, not unplayable", err)
	}
	if got, want := len(f.players()), maxSessions; got != want {
		t.Errorf("player requests = %d, want %d: %s", got, want, f.clients())
	}
}

// failingProbes fails every googlevideo request in transport, like a
// dropped connection, which net/http reports with the URL.
type failingProbes struct{ http.RoundTripper }

func (f failingProbes) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path == "/videoplayback" {
		return nil, errors.New("connection reset")
	}
	return f.RoundTripper.RoundTrip(r)
}

// A stream URL carries its signature, so errors, which logs and toasts
// show, must not.
func TestErrorKeepsURLsOut(t *testing.T) {
	f := newFake(t, fixed("player_ok.json"))
	y := f.resolver(Options{})
	y.opts.HTTP = &http.Client{Transport: failingProbes{f.srv.Client().Transport}}
	_, err := y.Resolve(t.Context(), okID)
	if err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("err = %v, want the probe's failure", err)
	}
	if s := err.Error(); strings.Contains(s, "videoplayback") || strings.Contains(s, "expire=") {
		t.Errorf("a stream URL leaked: %s", s)
	}
}

func TestNoProbe(t *testing.T) {
	f := newFake(t, fixed("player_ok.json"))
	f.media = func(int) int { return 403 }
	if _, err := f.resolver(Options{NoProbe: true}).Resolve(t.Context(), okID); err != nil {
		t.Fatal(err)
	}
	if n := len(f.probeRanges()); n != 0 {
		t.Errorf("probes = %d, want none", n)
	}
}

func TestCache(t *testing.T) {
	f := newFake(t, fixed("player_ok.json"))
	y := f.resolver(Options{})
	now := okExpires.Add(-5 * time.Hour)
	var mu sync.Mutex
	y.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	set := func(t time.Time) { mu.Lock(); now = t; mu.Unlock() }
	resolve := func(wantRequests int) {
		t.Helper()
		s, err := y.Resolve(t.Context(), okID)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(f.players()); n != wantRequests {
			t.Fatalf("player requests = %d, want %d", n, wantRequests)
		}
		// Callers get their own Header, so the cached one cannot race.
		s.Header.Set("User-Agent", "changed")
	}

	resolve(1)
	resolve(1)
	if got := len(f.probeRanges()); got != 1 {
		t.Errorf("probes = %d, want 1: a cached stream is not probed again", got)
	}
	set(okExpires.Add(-reuseMargin - time.Second))
	resolve(1)
	// Too close to expiry to outlast a song: resolved again.
	set(okExpires.Add(-reuseMargin))
	resolve(2)
	set(okExpires.Add(-2 * time.Hour))
	y.Forget(okID)
	resolve(3)
	s, _ := y.Resolve(t.Context(), okID)
	if got := s.Header.Get("User-Agent"); got != visionOS.UserAgent {
		t.Errorf("cached User-Agent = %q, changed by a caller", got)
	}
}

// Songs cached share their client's Header, which TestCache shows no
// caller gets to write; another Header is kept as it is.
func TestCacheSharesHeader(t *testing.T) {
	pr := okPlayer(t)
	now := okExpires.Add(-5 * time.Hour)
	y := New(Options{})
	y.now = func() time.Time { return now }
	keep := func(id string, header http.Header) http.Header {
		t.Helper()
		s, err := pr.stream(visionOS, now)
		if err != nil {
			t.Fatal(err)
		}
		if header != nil {
			s.Header = header
		}
		y.mu.Lock()
		defer y.mu.Unlock()
		return y.keepLocked(id, s).Header
	}
	first, second := keep("a", nil), keep("b", nil)
	if reflect.ValueOf(first).UnsafePointer() != reflect.ValueOf(second).UnsafePointer() {
		t.Error("two songs of one client cache a Header each")
	}
	other := keep("c", http.Header{"User-Agent": {"yt-dlp"}})
	if got := other.Get("User-Agent"); got != "yt-dlp" {
		t.Errorf("User-Agent = %q, want the stream's own", got)
	}
	if got := y.cache["a"].Header.Get("User-Agent"); got != visionOS.UserAgent {
		t.Errorf("first song's User-Agent = %q, want VisionOS's", got)
	}
}

func TestErrorsAreNotCached(t *testing.T) {
	f := newFake(t, fixed("player_error.json"))
	y := f.resolver(Options{})
	for range 2 {
		if _, err := y.Resolve(t.Context(), okID); err == nil {
			t.Fatal("want an error")
		}
	}
	if n := len(f.players()); n != 2 {
		t.Errorf("player requests = %d, want 2", n)
	}
}

func TestConcurrentDedupe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFake(t, fixed("player_ok.json"))
		f.gate = make(chan struct{})
		f.arrived = make(chan playerReq, 100)
		y := f.resolver(Options{})

		const n = 20
		var wg sync.WaitGroup
		urls := make([]string, n)
		errs := make([]error, n)
		for i := range n {
			wg.Go(func() {
				s, err := y.Resolve(t.Context(), okID)
				urls[i], errs[i] = s.URL, err
			})
		}
		<-f.arrived
		// Let the others join the call in flight before it finishes.
		synctest.Wait()
		y.mu.Lock()
		c := y.calls[okID]
		joined := c != nil && c.waiters == n
		y.mu.Unlock()
		if !joined {
			t.Fatal("not every caller joined the call in flight")
		}
		close(f.gate)
		wg.Wait()
		for i := range n {
			if errs[i] != nil || urls[i] != urls[0] {
				t.Fatalf("caller %d: %q, %v; want %q", i, urls[i], errs[i], urls[0])
			}
		}
		if got := len(f.players()); got != 1 {
			t.Errorf("player requests = %d, want 1", got)
		}
	})
}

func TestCancel(t *testing.T) {
	t.Run("last caller cancels the resolve", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := newFake(t, fixed("player_ok.json"))
			f.gate = make(chan struct{})
			f.arrived = make(chan playerReq, 10)
			y := f.resolver(Options{})
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() {
				_, err := y.Resolve(ctx, okID)
				done <- err
			}()
			<-f.arrived
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", err)
			}
			synctest.Wait()
			select {
			case <-f.canceled:
			default:
				t.Fatal("the player request was not canceled")
			}
			// A new caller starts afresh rather than joining the canceled call.
			close(f.gate)
			if _, err := y.Resolve(t.Context(), okID); err != nil {
				t.Fatal(err)
			}
			// The canceled resolve does not move on to the next client.
			if got := f.clients(); got != "VISIONOS() VISIONOS(VISITOR_OK)" && got != "VISIONOS() VISIONOS()" {
				t.Errorf("asked %s, want VISIONOS twice", got)
			}
		})
	})

	t.Run("others keep waiting", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := newFake(t, fixed("player_ok.json"))
			f.gate = make(chan struct{})
			f.arrived = make(chan playerReq, 10)
			y := f.resolver(Options{})
			ctx, cancel := context.WithCancel(t.Context())
			quit := make(chan error, 1)
			stay := make(chan error, 1)
			go func() {
				_, err := y.Resolve(ctx, okID)
				quit <- err
			}()
			<-f.arrived
			go func() {
				_, err := y.Resolve(t.Context(), okID)
				stay <- err
			}()
			synctest.Wait()
			y.mu.Lock()
			joined := y.calls[okID] != nil && y.calls[okID].waiters == 2
			y.mu.Unlock()
			if !joined {
				t.Fatal("the second caller did not join")
			}
			cancel()
			if err := <-quit; !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", err)
			}
			close(f.gate)
			if err := <-stay; err != nil {
				t.Fatalf("the caller who stayed got %v", err)
			}
			if got := len(f.players()); got != 1 {
				t.Errorf("player requests = %d, want 1", got)
			}
		})
	})

	t.Run("already canceled", func(t *testing.T) {
		f := newFake(t, fixed("player_ok.json"))
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := f.resolver(Options{}).Resolve(ctx, okID); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if n := len(f.players()); n != 0 {
			t.Errorf("player requests = %d, want none", n)
		}
	})
}
