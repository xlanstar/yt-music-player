package innertube

import (
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func server(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Client{
		HTTP:    srv.Client(),
		BaseURL: srv.URL + "/youtubei/v1/",
		HL:      "en",
		GL:      "US",
		Backoff: time.Millisecond,
	}
}

// callBody is Call keeping the response body.
func callBody(ctx context.Context, c *Client, endpoint string, body map[string]any) ([]byte, error) {
	var data []byte
	err := c.Call(ctx, endpoint, body, func(r io.Reader) (err error) {
		data, err = io.ReadAll(r)
		return err
	})
	return data, err
}

func TestCallRequest(t *testing.T) {
	var got struct {
		method, path, query string
		header              http.Header
		body                map[string]any
	}
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path, got.query, got.header = r.Method, r.URL.Path, r.URL.RawQuery, r.Header
		data, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(data, &got.body); err != nil {
			t.Errorf("body is not JSON: %v", err)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	c.VisitorID = "visitor"
	c.Auth = &Auth{}
	c.Auth.Set(func(r *http.Request) error {
		r.Header.Set("Authorization", "SAPISIDHASH test")
		return nil
	})
	in := map[string]any{"browseId": "MPREb_x"}
	out, err := callBody(t.Context(), c, "browse", in)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"ok":true}` {
		t.Errorf("response = %s", out)
	}
	if _, ok := in["context"]; ok {
		t.Error("Call modified the caller's body")
	}
	if got.method != http.MethodPost || got.path != "/youtubei/v1/browse" || got.query != "prettyPrint=false" {
		t.Errorf("request = %s %s?%s", got.method, got.path, got.query)
	}
	for k, want := range map[string]string{
		"Content-Type":      "application/json",
		"Origin":            "https://music.youtube.com",
		"Referer":           "https://music.youtube.com/",
		"User-Agent":        UserAgent,
		"X-Goog-Visitor-Id": "visitor",
		"Authorization":     "SAPISIDHASH test",
	} {
		if v := got.header.Get(k); v != want {
			t.Errorf("header %s = %q, want %q", k, v, want)
		}
	}
	if got.body["browseId"] != "MPREb_x" {
		t.Errorf("browseId = %v", got.body["browseId"])
	}
	client, _ := got.body["context"].(map[string]any)["client"].(map[string]any)
	if client["clientName"] != "WEB_REMIX" || client["hl"] != "en" || client["gl"] != "US" {
		t.Errorf("context.client = %v", client)
	}
	version, _ := client["clientVersion"].(string)
	if !regexp.MustCompile(`^1\.\d{8}\.01\.00$`).MatchString(version) {
		t.Errorf("clientVersion = %q", version)
	}
}

func TestCallRetries5xx(t *testing.T) {
	var calls atomic.Int32
	c := server(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	if _, err := callBody(t.Context(), c, "search", nil); err != nil {
		t.Fatal(err)
	}
	if n := calls.Load(); n != 3 {
		t.Errorf("calls = %d, want 3", n)
	}
}

func TestCallGivesUp(t *testing.T) {
	var calls atomic.Int32
	c := server(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	c.Retries = 2
	_, err := callBody(t.Context(), c, "search", nil)
	if statusErr, ok := errors.AsType[*StatusError](err); !ok || statusErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("err = %v, want a 503 StatusError", err)
	}
	if n := calls.Load(); n != 3 {
		t.Errorf("calls = %d, want 3", n)
	}
}

// Only a 429 or a 5xx is asked again.
func TestCallRetriesOnlyWhatMaySucceed(t *testing.T) {
	for status, want := range map[int]int32{
		http.StatusTooManyRequests:     2,
		http.StatusInternalServerError: 2,
		http.StatusBadGateway:          2,
		http.StatusBadRequest:          1,
		http.StatusUnauthorized:        1,
		http.StatusNotFound:            1,
	} {
		var calls atomic.Int32
		c := server(t, func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.WriteHeader(status)
		})
		c.Retries = 1
		if _, err := callBody(t.Context(), c, "browse", nil); err == nil {
			t.Errorf("HTTP %d: no error", status)
		}
		if n := calls.Load(); n != want {
			t.Errorf("HTTP %d: %d calls, want %d", status, n, want)
		}
	}
}

func TestCallStatusError(t *testing.T) {
	var calls atomic.Int32
	c := server(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":400,"message":"Request contains an invalid argument."}}`))
	})
	_, err := callBody(t.Context(), c, "browse", nil)
	statusErr, ok := errors.AsType[*StatusError](err)
	if !ok {
		t.Fatalf("err = %v, want a StatusError", err)
	}
	if statusErr.StatusCode != http.StatusBadRequest || statusErr.Message != "Request contains an invalid argument." {
		t.Errorf("err = %+v", statusErr)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("a 400 was retried: calls = %d", n)
	}
}

func TestCallCanceledDuringBackoff(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	c.Backoff = time.Hour
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(20*time.Millisecond, cancel)
	start := time.Now()
	_, err := callBody(ctx, c, "search", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("Call returned after %v", d)
	}
}

func TestCallCanceledInFlight(t *testing.T) {
	release := make(chan struct{})
	c := server(t, func(http.ResponseWriter, *http.Request) { <-release })
	// Cleanups run last-in first-out: the handler returns before the server closes.
	t.Cleanup(func() { close(release) })
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := callBody(ctx, c, "search", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestAuthSigns(t *testing.T) {
	var sent atomic.Pointer[http.Header]
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Clone()
		sent.Store(&h)
		_, _ = w.Write([]byte(`{}`))
	})
	authorization := func() string { return sent.Load().Get("Authorization") }
	call := func() {
		t.Helper()
		if _, err := callBody(t.Context(), c, "browse", nil); err != nil {
			t.Fatal(err)
		}
	}
	call()
	if authorization() != "" {
		t.Errorf("without Auth: Authorization = %v", authorization())
	}
	c.Auth = &Auth{}
	call()
	if c.Auth.IsSignedIn() || authorization() != "" {
		t.Errorf("empty Auth: Authorization = %v", authorization())
	}
	c.Auth.Set(func(r *http.Request) error {
		r.Header.Set("Cookie", r.Header.Get("Cookie")+"; SID=x")
		r.Header.Set("Authorization", "signed")
		return nil
	})
	// A copy, as made for a visitor, shares the credentials.
	visitorCopy := *c
	visitorCopy.VisitorID = "v"
	if _, err := callBody(t.Context(), &visitorCopy, "browse", nil); err != nil {
		t.Fatal(err)
	}
	if cookie := sent.Load().Get("Cookie"); !c.Auth.IsSignedIn() || authorization() != "signed" || cookie != "SOCS=CAI; SID=x" {
		t.Errorf("signed: Authorization %v, Cookie %v", authorization(), cookie)
	}
	c.Auth.Set(nil)
	call()
	if c.Auth.IsSignedIn() || authorization() != "" {
		t.Errorf("signed out: Authorization = %v", authorization())
	}
	var nilAuth *Auth
	if nilAuth.IsSignedIn() {
		t.Error("nil Auth is signed in")
	}
}

func TestAuthError(t *testing.T) {
	boom := errors.New("boom")
	c := server(t, func(http.ResponseWriter, *http.Request) { t.Error("request sent despite an Auth error") })
	c.Auth = &Auth{}
	c.Auth.Set(func(*http.Request) error { return boom })
	if _, err := callBody(t.Context(), c, "browse", nil); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestDecodeError(t *testing.T) {
	var requests atomic.Int32
	c := server(t, func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{}`))
	})
	boom := errors.New("boom")
	if err := c.Call(t.Context(), "browse", nil, func(io.Reader) error { return boom }); !errors.Is(err, boom) {
		t.Errorf("err = %v, want boom", err)
	}
	if n := requests.Load(); n != 1 {
		t.Errorf("%d requests, want 1: a decode error is not retried", n)
	}
}

func TestUnauthorized(t *testing.T) {
	for _, tc := range []struct {
		status int
		signed bool
		want   bool
	}{
		{http.StatusUnauthorized, false, true},
		{http.StatusUnauthorized, true, true},
		{http.StatusForbidden, true, true},
		// Anonymous 403s are region locks and the like, not credentials.
		{http.StatusForbidden, false, false},
		{http.StatusNotFound, true, false},
	} {
		var calls atomic.Int32
		c := server(t, func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			http.Error(w, `{"error":{"message":"You must be signed in to perform this operation."}}`, tc.status)
		})
		c.Auth = &Auth{}
		if tc.signed {
			c.Auth.Set(func(*http.Request) error { return nil })
		}
		_, err := callBody(t.Context(), c, "like/like", nil)
		if statusErr, ok := errors.AsType[*StatusError](err); !ok || statusErr.Authenticated != tc.signed ||
			errors.Is(err, ErrUnauthorized) != tc.want {
			t.Errorf("%d signed %v: err = %v, want unauthorized %v", tc.status, tc.signed, err, tc.want)
		}
		if calls.Load() != 1 {
			t.Errorf("%d: %d attempts; refused credentials are not retried", tc.status, calls.Load())
		}
	}
}

// TestAuthSwapRace swaps credentials while calls run; -race checks it.
func TestAuthSwapRace(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" && got != "a" && got != "b" {
			t.Errorf("Authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{}`))
	})
	c.Auth = &Auth{}
	sign := func(v string) func(*http.Request) error {
		return func(r *http.Request) error {
			r.Header.Set("Authorization", v)
			return nil
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 200 {
			switch i % 3 {
			case 0:
				c.Auth.Set(sign("a"))
			case 1:
				c.Auth.Set(sign("b"))
			default:
				c.Auth.Set(nil)
			}
		}
	}()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 10 {
				if _, err := callBody(t.Context(), c, "browse", nil); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	<-done
}
