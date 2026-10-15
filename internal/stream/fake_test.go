package stream

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fixtureHost is where the fixtures' URLs point; the fake's in-memory
// client sends every host to the fake.
const fixtureHost = "https://rr1---sn-fixture.googlevideo.com"

// okID is the video in player_ok.json.
const okID = "FGBhQbmPwH8"

// okExpires is the expire parameter of player_ok.json's URLs.
var okExpires = time.Unix(1791988454, 0)

type playerReq struct {
	Client, Version, VideoID string
	// Visitor is the context's visitorData, VisitorHeader X-Goog-Visitor-Id.
	Visitor, VisitorHeader string
	ClientID, UserAgent    string
	Body                   map[string]any
}

// fakeTube is the player endpoint and googlevideo behind one server on
// httptest's in-memory network, which a synctest bubble can wait on.
type fakeTube struct {
	t   *testing.T
	srv *httptest.Server

	// answer names the fixture a player request gets.
	answer func(playerReq) string
	// gate, if set, holds each player request until closed or the request
	// is canceled; arrived receives each one held.
	gate     chan struct{}
	arrived  chan playerReq
	canceled chan struct{}
	// media returns googlevideo's status for the nth probe; nil means 206.
	media func(n int) int

	mu       sync.Mutex
	requests []playerReq
	probes   []string // Range headers
}

func newFake(t *testing.T, answer func(playerReq) string) *fakeTube {
	t.Helper()
	f := &fakeTube{t: t, answer: answer, canceled: make(chan struct{}, 100)}
	f.srv = httptest.NewTestServer(t, http.HandlerFunc(f.serve))
	return f
}

func fixed(name string) func(playerReq) string {
	return func(playerReq) string { return name }
}

func byClient(m map[string]string) func(playerReq) string {
	return func(r playerReq) string { return m[r.Client] }
}

func (f *fakeTube) resolver(opts Options) *YouTube {
	opts.HTTP = f.srv.Client()
	y := New(opts)
	// The fixtures' URLs expire at a fixed time, which the real clock passes.
	y.now = func() time.Time { return okExpires.Add(-5 * time.Hour) }
	return y
}

func (f *fakeTube) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/youtubei/v1/player":
		f.servePlayer(w, r)
	case "/videoplayback":
		f.mu.Lock()
		f.probes = append(f.probes, r.Header.Get("Range"))
		n := len(f.probes)
		f.mu.Unlock()
		status := http.StatusPartialContent
		if f.media != nil {
			status = f.media(n)
		}
		w.WriteHeader(status)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeTube) servePlayer(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.UnmarshalRead(r.Body, &body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	client, _ := body["context"].(map[string]any)["client"].(map[string]any)
	str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	req := playerReq{
		Client:        str(client, "clientName"),
		Version:       str(client, "clientVersion"),
		VideoID:       str(body, "videoId"),
		Visitor:       str(client, "visitorData"),
		VisitorHeader: r.Header.Get("X-Goog-Visitor-Id"),
		ClientID:      r.Header.Get("X-YouTube-Client-Name"),
		UserAgent:     r.Header.Get("User-Agent"),
		Body:          body,
	}
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	if f.gate != nil {
		if f.arrived != nil {
			f.arrived <- req
		}
		select {
		case <-f.gate:
		case <-r.Context().Done():
			f.canceled <- struct{}{}
			return
		}
	}
	data, err := os.ReadFile(filepath.Join("testdata", f.answer(req)))
	if err != nil {
		f.t.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(data)
}

func (f *fakeTube) players() []playerReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]playerReq(nil), f.requests...)
}

// clients lists the clients asked, in order, as "NAME(visitorData)".
func (f *fakeTube) clients() string {
	var b strings.Builder
	for i, r := range f.players() {
		if i > 0 {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "%s(%s)", r.Client, r.Visitor)
	}
	return b.String()
}

func (f *fakeTube) probeRanges() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.probes...)
}
