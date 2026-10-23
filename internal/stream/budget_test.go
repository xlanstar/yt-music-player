package stream

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"weak"
	"yt-music-player/internal/budget"
)

func testdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func okPlayer(t *testing.T) *playerResponse {
	t.Helper()
	pr, err := parsePlayer(bytes.NewReader(testdata(t, "player_ok.json")))
	if err != nil {
		t.Fatal(err)
	}
	return pr
}

func TestBudgetParsePlayer(t *testing.T) {
	data := testdata(t, "player_ok.json")
	var r bytes.Reader
	// 19 allocs, 4.1 KB today; reading it all first made 28 and 15.4 KB.
	budget.Within(t, budget.Cost{Allocs: 22, Bytes: 4.8e3}, 100, func() {
		r.Reset(data)
		sinkPlayer, sinkErr = parsePlayer(&r)
	})
}

func TestBudgetPlayerBody(t *testing.T) {
	// 2 allocs today: the encoder's buffer and the body.
	budget.Allocs(t, 2, 100, func() { sinkBytes, sinkErr = playerBody(visionOS, okID, "CgtWSVNJVE9SXzEyMw%3D%3D") })
}

func TestBudgetPickFormat(t *testing.T) {
	formats := okPlayer(t).StreamingData.AdaptiveFormats
	// 3 allocs today: url.Parse of the three Opus formats' URLs (it was
	// two, of the AAC ones, when it picked those).
	budget.Allocs(t, 4, 100, func() { sinkFormat, sinkBool = pickFormat(formats) })
}

func TestBudgetExpiry(t *testing.T) {
	url := okPlayer(t).StreamingData.AdaptiveFormats[2].URL
	now := time.Unix(1_000_000, 0)
	// 1 alloc today: url.Parse's.
	budget.Allocs(t, 1, 100, func() { sinkTime = expiry(url, 21540, now) })
}

func TestBudgetStream(t *testing.T) {
	pr := okPlayer(t)
	now := okExpires.Add(-5 * time.Hour)
	// 7 allocs today, one more since pickFormat parses the three Opus
	// formats' URLs, not the two AAC ones.
	budget.Allocs(t, 9, 100, func() { sinkStream, sinkErr = pr.stream(visionOS, now) })
}

func TestBudgetPlayability(t *testing.T) {
	pr, err := parsePlayer(bytes.NewReader(testdata(t, "player_unplayable.json")))
	if err != nil {
		t.Fatal(err)
	}
	// 2 allocs today: the error and its reason.
	budget.Allocs(t, 2, 100, func() { sinkPE = pr.playability(visionOS.Name) })
}

func TestBudgetParseYTDLP(t *testing.T) {
	data := testdata(t, "ytdlp.json")
	now := time.Unix(1_000_000, 0)
	// 12 allocs today.
	budget.Allocs(t, 14, 100, func() { sinkStream, sinkErr = parseYTDLP(data, now) })
}

// A cache hit should cost little more than the copy of its Header.
func TestBudgetResolveCached(t *testing.T) {
	y := New(Options{})
	now := okExpires.Add(-5 * time.Hour)
	y.now = func() time.Time { return now }
	s, err := okPlayer(t).stream(visionOS, now)
	if err != nil {
		t.Fatal(err)
	}
	y.cache[okID] = s
	ctx := t.Context()
	// 3 allocs today.
	budget.Allocs(t, 4, 100, func() { sinkStream, sinkErr = y.Resolve(ctx, okID) })
}

// TestBudgetLiveCache holds the heap the cache keeps a song, which ytmd
// keeps for the five hours its URL lives: a hundred songs, listening on.
// With the resolver, less once it is collected.
func TestBudgetLiveCache(t *testing.T) {
	if budget.Race {
		t.Skip("the race detector allocates on its own")
	}
	pr := okPlayer(t)
	now := okExpires.Add(-5 * time.Hour)
	// googlevideo's URLs are about 1.3 KB long, the fixture's cut short.
	url := pr.StreamingData.AdaptiveFormats[5].URL + strings.Repeat("&p=x", 280)
	const songs = 100
	y := New(Options{})
	y.now = func() time.Time { return now }
	for i := range songs {
		s, err := pr.stream(visionOS, now)
		if err != nil {
			t.Fatal(err)
		}
		s.URL = strings.Clone(url)
		y.mu.Lock()
		y.keepLocked("song"+strconv.Itoa(i), s)
		y.mu.Unlock()
	}
	// A GC first: what json's pools keep goes only at the second.
	budget.LiveHeap()
	with := budget.LiveHeap()
	gone := weak.Make(y)
	y = nil
	if !budget.IsCollected(gone) {
		t.Fatal("the dropped resolver stays reachable")
	}
	perSong := (with - budget.LiveHeap()) / songs
	t.Logf("%d B a song", perSong)
	// 1559 B today, 1408 of them the URL; with a Header each, 1970.
	if perSong > 1800 {
		t.Errorf("%d B a song, budget 1800", perSong)
	}
}
