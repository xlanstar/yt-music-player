package stream

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

var (
	sinkPlayer *playerResponse
	sinkFormat format
	sinkBool   bool
	sinkBytes  []byte
	sinkTime   time.Time
	sinkStream Stream
	sinkErr    error
	sinkPE     *PlayabilityError
	// Atomic: parallel benchmarks write it.
	sinkShared atomic.Int64
)

// benchNow leaves player_ok.json's URLs hours to live, so its stream is
// cached.
var benchNow = okExpires.Add(-5 * time.Hour)

func readTestdata(b *testing.B, name string) []byte {
	b.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		b.Fatal(err)
	}
	return data
}

// fullPlayer is player_ok.json with a music video's full format ladder:
// 24 formats rather than the fixture's 6.
func fullPlayer(b *testing.B) []byte {
	b.Helper()
	var response map[string]any
	if err := json.Unmarshal(readTestdata(b, "player_ok.json"), &response); err != nil {
		b.Fatal(err)
	}
	streamingData := response["streamingData"].(map[string]any)
	formats := streamingData["adaptiveFormats"].([]any)
	for len(formats) < 24 {
		formats = append([]any{formats[0]}, formats...)
	}
	streamingData["adaptiveFormats"] = formats
	data, err := json.Marshal(response)
	if err != nil {
		b.Fatal(err)
	}
	return data
}

func benchPlayers(b *testing.B) []struct {
	name string
	data []byte
} {
	b.Helper()
	return []struct {
		name string
		data []byte
	}{
		{"ok", readTestdata(b, "player_ok.json")},
		{"cipher", readTestdata(b, "player_cipher.json")},
		{"login", readTestdata(b, "player_login.json")},
		{"full", fullPlayer(b)},
	}
}

func mustParsePlayer(b *testing.B, data []byte) *playerResponse {
	b.Helper()
	pr, err := parsePlayer(data)
	if err != nil {
		b.Fatal(err)
	}
	return pr
}

func BenchmarkParsePlayer(b *testing.B) {
	for _, tc := range benchPlayers(b) {
		b.Run(tc.name, func(b *testing.B) {
			b.SetBytes(int64(len(tc.data)))
			b.ReportAllocs()
			for b.Loop() {
				sinkPlayer, sinkErr = parsePlayer(tc.data)
			}
		})
	}
}

func BenchmarkPlayerBody(b *testing.B) {
	for _, c := range []Client{VisionOS, AndroidVR} {
		b.Run(c.Name, func(b *testing.B) {
			data, _ := playerBody(c, okID, "CgtWSVNJVE9SXzEyMw%3D%3D")
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			for b.Loop() {
				sinkBytes, sinkErr = playerBody(c, okID, "CgtWSVNJVE9SXzEyMw%3D%3D")
			}
		})
	}
}

func BenchmarkPickFormat(b *testing.B) {
	for _, tc := range benchPlayers(b) {
		if tc.name == "login" {
			continue // No formats.
		}
		b.Run(tc.name, func(b *testing.B) {
			formats := mustParsePlayer(b, tc.data).StreamingData.AdaptiveFormats
			b.ReportAllocs()
			for b.Loop() {
				sinkFormat, sinkBool = pickFormat(formats)
			}
		})
	}
}

func BenchmarkExpiry(b *testing.B) {
	pr := mustParsePlayer(b, readTestdata(b, "player_ok.json"))
	for _, tc := range []struct{ name, url string }{
		{"expire", pr.StreamingData.AdaptiveFormats[2].URL},
		{"expires_in", "https://rr1---sn-fixture.googlevideo.com/videoplayback?id=o-FIXTURE&itag=140&source=youtube"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkTime = expiry(tc.url, 21540, benchNow)
			}
		})
	}
}

func BenchmarkStream(b *testing.B) {
	for _, tc := range benchPlayers(b) {
		if tc.name != "ok" && tc.name != "full" {
			continue
		}
		b.Run(tc.name, func(b *testing.B) {
			pr := mustParsePlayer(b, tc.data)
			b.ReportAllocs()
			for b.Loop() {
				sinkStream, sinkErr = pr.stream(VisionOS, benchNow)
			}
		})
	}
}

func BenchmarkPlayability(b *testing.B) {
	for _, name := range []string{"ok", "login", "unplayable"} {
		b.Run(name, func(b *testing.B) {
			pr := mustParsePlayer(b, readTestdata(b, "player_"+name+".json"))
			b.ReportAllocs()
			for b.Loop() {
				sinkPE = pr.playability(VisionOS.Name)
			}
		})
	}
}

func BenchmarkParseYTDLP(b *testing.B) {
	data := readTestdata(b, "ytdlp.json")
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		sinkStream, sinkErr = parseYTDLP(data, benchNow)
	}
}

// benchTube serves from memory, so BenchmarkResolve measures the resolver
// and its HTTP, not disk.
func benchTube(b *testing.B) *httptest.Server {
	b.Helper()
	data := readTestdata(b, "player_ok.json")
	var srv *httptest.Server
	var player []byte
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		switch r.URL.Path {
		case "/youtubei/v1/player":
			_, _ = w.Write(player)
		case "/videoplayback":
			w.WriteHeader(http.StatusPartialContent)
		default:
			http.NotFound(w, r)
		}
	}))
	player = bytes.ReplaceAll(data, []byte(fixtureHost), []byte(srv.URL))
	b.Cleanup(srv.Close)
	return srv
}

func benchResolver(b *testing.B) *YouTube {
	b.Helper()
	srv := benchTube(b)
	y := New(Options{HTTP: srv.Client(), BaseURL: srv.URL + "/youtubei/v1", VisitorData: "SEED"})
	y.now = func() time.Time { return benchNow }
	return y
}

func BenchmarkResolve(b *testing.B) {
	// A player request and a probe each time: Forget empties the cache.
	b.Run("miss", func(b *testing.B) {
		y := benchResolver(b)
		ctx := b.Context()
		b.ReportAllocs()
		for b.Loop() {
			y.Forget(okID)
			if sinkStream, sinkErr = y.Resolve(ctx, okID); sinkErr != nil {
				b.Fatal(sinkErr)
			}
		}
	})
	b.Run("cached", func(b *testing.B) {
		y := benchResolver(b)
		ctx := b.Context()
		if _, err := y.Resolve(ctx, okID); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		for b.Loop() {
			sinkStream, sinkErr = y.Resolve(ctx, okID)
		}
	})
	// Every core resolving cached songs at once: the cache lock under
	// contention.
	b.Run("cached_parallel", func(b *testing.B) {
		y := benchResolver(b)
		ctx := b.Context()
		s, err := y.Resolve(ctx, okID)
		if err != nil {
			b.Fatal(err)
		}
		ids := make([]string, 64)
		y.mu.Lock()
		for i := range ids {
			ids[i] = fmt.Sprintf("bench%06d", i)
			y.cache[ids[i]] = s
		}
		y.mu.Unlock()
		var next atomic.Int64
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			i := int(next.Add(1))
			var total int64
			for pb.Next() {
				s, err := y.Resolve(ctx, ids[i%len(ids)])
				if err != nil {
					b.Error(err)
					return
				}
				total += s.Size
				i++
			}
			sinkShared.Add(total)
		})
	})
}
