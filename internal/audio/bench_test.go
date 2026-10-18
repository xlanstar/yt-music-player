package audio

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"yt-music-player/internal/budget"
)

var (
	sinkFloat float64
	sinkBool  bool
	sinkEvent Event
)

// The Fake as the app drives it, and the engine's parts on the fixture:
// the system's output is only measured live (live_test.go).

var benchStream = Media{URL: "https://rr1---sn-a5mekn6s.googlevideo.com/videoplayback?itag=251", Seconds: 213.1}

type fakeCall struct {
	name  string
	limit float64
	call  func()
}

// fakeCalls are what playback does to an engine per song. App frame tests
// drive the Fake, which must not count against them: Load allocates 2
// today (its note), the others 0.
func fakeCalls(f *Fake) []fakeCall {
	return []fakeCall{
		{"Load", 2, func() { f.Load(benchStream, 84) }},
		{"PlayPause", 0, func() { f.Play(); f.Pause() }},
		{"Position", 0, func() { sinkFloat = f.Position() }},
		{"SetInfo", 0, func() { f.SetInfo(Info{Title: "Midnight Arcade", Artist: "Lumen Drive", Seconds: 227}) }},
		{"Emit", 0, func() { f.Emit(Ready) }},
	}
}

func BenchmarkFake(b *testing.B) {
	f := NewFake(func(e Event) { sinkEvent = e })
	for _, tc := range fakeCalls(f) {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				tc.call()
				f.Calls = f.Calls[:0]
			}
		})
	}
}

func BenchmarkIsCommand(b *testing.B) {
	e := Event{Kind: CommandNext}
	b.ReportAllocs()
	for b.Loop() {
		sinkBool = e.IsCommand()
	}
}

var (
	sinkInt int
	sinkErr error
)

// memTransport serves data to range requests from memory, as googlevideo
// would: deterministic, unlike a socket.
type memTransport struct{ data []byte }

// RoundTrip answers "bytes=a-b" ranges, the source's only kind, with a
// view of data, as a socket would hand its bytes over without copies.
func (m memTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	from, to, _ := strings.Cut(strings.TrimPrefix(req.Header.Get("Range"), "bytes="), "-")
	start, _ := strconv.Atoi(from)
	end, _ := strconv.Atoi(to)
	end = min(end+1, len(m.data))
	return &http.Response{
		StatusCode:    http.StatusPartialContent,
		Header:        http.Header{"Content-Range": {"bytes " + from + "-" + strconv.Itoa(end-1) + "/" + strconv.Itoa(len(m.data))}},
		Body:          io.NopCloser(bytes.NewReader(m.data[start:end])),
		ContentLength: int64(end - start),
		Request:       req,
	}, nil
}

// sourceCases are a song's worth of bytes, and ten minutes' at 128 kbit/s:
// both stream through the same window of chunks.
var sourceCases = []struct {
	name string
	size int
	// limit is TestBudgetSourceRead's.
	limit budget.Cost
}{
	// 132 allocs today (requests, chunks), 1.49 MB: the window, made once,
	// and the download's buffer. Keeping the whole file took 4.27 MB, in
	// fewer, longer requests (103 allocs).
	{"4MB", 4 << 20, budget.Cost{Allocs: 152, Bytes: 1.72e6}},
	// 288 allocs today, 1.50 MB.
	{"10min", 10 << 20, budget.Cost{Allocs: 332, Bytes: 1.73e6}},
}

// readSource reads all of data through an httpSource, in the demuxer's
// reads of 16 KiB.
func readSource(tb testing.TB, client *http.Client, buf []byte) {
	src := newHTTPSource(client, "https://googlevideo.test/videoplayback", nil, nil)
	defer func() { _ = src.Close() }()
	for {
		n, err := src.Read(buf)
		sinkInt += n
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			tb.Fatal(err)
		}
	}
}

func BenchmarkSourceRead(b *testing.B) {
	for _, tc := range sourceCases {
		b.Run(tc.name, func(b *testing.B) {
			client := &http.Client{Transport: memTransport{testBytes(tc.size)}}
			buf := make([]byte, 16<<10)
			b.SetBytes(int64(tc.size))
			b.ReportAllocs()
			for b.Loop() {
				readSource(b, client, buf)
			}
		})
	}
}

// decodeThrough decodes s from the start to its end.
func decodeThrough(tb testing.TB, s *opusStream, buf []float32) {
	if err := s.SetPosition(0); err != nil {
		tb.Fatal(err)
	}
	for {
		n, err := s.Read(buf)
		sinkInt += n
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			tb.Fatal(err)
		}
	}
}

func BenchmarkOpusRead(b *testing.B) {
	s := openTone(b, toneFixtures[1])
	buf := make([]float32, 2048*2)
	b.ReportAllocs()
	for b.Loop() {
		decodeThrough(b, s, buf)
	}
	// Seconds of audio decoded per second.
	b.ReportMetric(6*float64(b.N)/b.Elapsed().Seconds(), "x-realtime")
}

func BenchmarkOpusSetPosition(b *testing.B) {
	s := openTone(b, toneFixtures[1])
	b.ReportAllocs()
	for b.Loop() {
		sinkErr = s.SetPosition(3 * opusRate)
	}
}
