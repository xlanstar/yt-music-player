package innertube

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"yt-music-player/internal/auth"
)

// Sinks keep the compiler from optimizing measured calls away.
var (
	sinkBytes  []byte
	sinkString string
	sinkBool   bool
	sinkReq    *http.Request
	sinkCtx    requestContext
	sinkWait   time.Duration
	// sinkShared is written by parallel benchmarks.
	sinkShared atomic.Bool
)

// benchCookie is shaped like a real session's, with made-up values.
const benchCookie = "VISITOR_INFO1_LIVE=fakevisitor; SID=fakesid; __Secure-3PSID=fake3psid; " +
	"SAPISID=fakeSAPISID/abc; __Secure-3PAPISID=fakeSAPISID/abc; SOCS=CAISEwgDEgk"

// benchBodies are the request bodies ytmusic sends most: a page, a
// continuation (tokens run to a few hundred bytes) and a like.
var benchBodies = []struct {
	name string
	body map[string]any
}{
	{"browse", map[string]any{"browseId": "MPREb_7ltM34kr0mH"}},
	{"continuation", map[string]any{"continuation": strings.Repeat("4qmFsgKhAhIMRkVtdXNpY19ob21l", 12)}},
	{"like", map[string]any{"target": map[string]any{"videoId": "uSFXOBaqiR8"}}},
}

// benchResponse is a response the size of a real page (tens of KB).
var benchResponse = []byte(`{"responseContext":{"visitorData":"CgtWSVNJVE9SXzEyMw%3D%3D"},"contents":[` +
	strings.Repeat(`{"musicResponsiveListItemRenderer":{"flexColumns":[{"text":{"runs":[{"text":"Song"}]}}]}},`, 400) +
	`{}]}`)

func benchClient() *Client {
	return &Client{HL: "en", GL: "US", ClientVersion: "1.20260101.01.00"}
}

func signer(b *testing.B) func(*http.Request) error {
	b.Helper()
	creds, err := auth.Parse("X-Goog-AuthUser: 1\nCookie: " + benchCookie)
	if err != nil {
		b.Fatal(err)
	}
	return creds.Decorator(nil)
}

func BenchmarkPayload(b *testing.B) {
	c := benchClient()
	for _, tc := range benchBodies {
		b.Run(tc.name, func(b *testing.B) {
			data, _ := c.payload(tc.body)
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			for b.Loop() {
				sinkBytes, _ = c.payload(tc.body)
			}
		})
	}
}

func BenchmarkContext(b *testing.B) {
	for _, tc := range []struct {
		name    string
		version string
	}{{"pinned", "1.20260101.01.00"}, {"dated", ""}} {
		b.Run(tc.name, func(b *testing.B) {
			c := &Client{HL: "en", GL: "US", ClientVersion: tc.version}
			b.ReportAllocs()
			for b.Loop() {
				sinkCtx = c.context()
			}
		})
	}
}

func BenchmarkNewRequest(b *testing.B) {
	data, _ := benchClient().payload(benchBodies[0].body)
	const url = DefaultBaseURL + "browse?prettyPrint=false"
	for _, tc := range []struct {
		name  string
		setup func(*Client)
	}{
		{"anonymous", func(*Client) {}},
		{"visitor", func(c *Client) { c.VisitorID = "CgtWSVNJVE9SXzEyMw%3D%3D" }},
		{"signed", func(c *Client) {
			c.Auth = &Auth{}
			c.Auth.Set(signer(b))
		}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			c := benchClient()
			tc.setup(c)
			ctx := b.Context()
			b.ReportAllocs()
			for b.Loop() {
				sinkReq, sinkBool, _ = c.newRequest(ctx, "browse", url, data)
			}
		})
	}
}

// roundTripper answers without a socket, so BenchmarkCall/transport
// measures Call alone.
type roundTripper struct{ resp []byte }

func (rt roundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(rt.resp)),
	}, nil
}

func benchServer(b *testing.B, h http.HandlerFunc) *Client {
	b.Helper()
	srv := httptest.NewServer(h)
	b.Cleanup(srv.Close)
	c := benchClient()
	c.HTTP = srv.Client()
	c.BaseURL = srv.URL + "/youtubei/v1/"
	return c
}

func serveResponse(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(benchResponse)
}

// discard reads a response as a decoder would, keeping nothing.
func discard(r io.Reader) error {
	_, err := io.Copy(io.Discard, r)
	return err
}

func BenchmarkCall(b *testing.B) {
	body := benchBodies[0].body
	run := func(b *testing.B, c *Client) {
		b.Helper()
		ctx := b.Context()
		b.SetBytes(int64(len(benchResponse)))
		b.ReportAllocs()
		for b.Loop() {
			var err error
			if err = c.Call(ctx, "browse", body, discard); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.Run("transport", func(b *testing.B) {
		c := benchClient()
		c.HTTP = &http.Client{Transport: roundTripper{benchResponse}}
		run(b, c)
	})
	b.Run("httptest", func(b *testing.B) {
		run(b, benchServer(b, serveResponse))
	})
	b.Run("signed", func(b *testing.B) {
		c := benchServer(b, serveResponse)
		c.Auth = &Auth{}
		c.Auth.Set(signer(b))
		run(b, c)
	})
	// Every call is refused once with a 503, then retried at once: the
	// cost of the retry path, not of waiting.
	b.Run("retry", func(b *testing.B) {
		var requests atomic.Int64
		c := benchServer(b, func(w http.ResponseWriter, r *http.Request) {
			if requests.Add(1)%2 == 1 {
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			serveResponse(w, r)
		})
		c.Backoff = time.Nanosecond
		run(b, c)
	})
}

// BenchmarkAuthSet swaps credentials, as signing in and out does while
// calls run.
func BenchmarkAuthSet(b *testing.B) {
	var a Auth
	signs := [2]func(*http.Request) error{signer(b), signer(b)}
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		a.Set(signs[i&1])
		i++
	}
	sinkBool = a.IsSignedIn()
}

// BenchmarkAuthSignedIn reads the credentials, as every attempt does,
// from every core at once.
func BenchmarkAuthSignedIn(b *testing.B) {
	a := &Auth{}
	a.Set(signer(b))
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		var signed bool
		for pb.Next() {
			signed = a.IsSignedIn()
		}
		sinkShared.Store(signed)
	})
}

func BenchmarkErrorMessage(b *testing.B) {
	body := []byte(`{"error":{"code":400,"message":"Request contains an invalid argument.",` +
		`"errors":[{"message":"Request contains an invalid argument.","domain":"global","reason":"badRequest"}],` +
		`"status":"INVALID_ARGUMENT"}}`)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	for b.Loop() {
		sinkString = errorMessage(body)
	}
}

func BenchmarkRetryAfter(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		sinkWait = retryAfter(" 5 ")
	}
}
