package stream

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
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
