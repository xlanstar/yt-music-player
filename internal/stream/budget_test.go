package stream

import (
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
	pr, err := parsePlayer(testdata(t, "player_ok.json"))
	if err != nil {
		t.Fatal(err)
	}
	return pr
}

func TestBudgetParsePlayer(t *testing.T) {
	data := testdata(t, "player_ok.json")
	// 19 allocs today.
	budget.Allocs(t, 22, 100, func() { sinkPlayer, sinkErr = parsePlayer(data) })
}

func TestBudgetPlayerBody(t *testing.T) {
	// 2 allocs today: the encoder's buffer and the body.
	budget.Allocs(t, 2, 100, func() { sinkBytes, sinkErr = playerBody(VisionOS, okID, "CgtWSVNJVE9SXzEyMw%3D%3D") })
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
	budget.Allocs(t, 9, 100, func() { sinkStream, sinkErr = pr.stream(VisionOS, now) })
}

func TestBudgetPlayability(t *testing.T) {
	pr, err := parsePlayer(testdata(t, "player_unplayable.json"))
	if err != nil {
		t.Fatal(err)
	}
	// 2 allocs today: the error and its reason.
	budget.Allocs(t, 2, 100, func() { sinkPE = pr.playability(VisionOS.Name) })
}

func TestBudgetParseYTDLP(t *testing.T) {
	data := testdata(t, "ytdlp.json")
	now := time.Unix(1_000_000, 0)
	// 13 allocs today.
	budget.Allocs(t, 15, 100, func() { sinkStream, sinkErr = parseYTDLP(data, now) })
}

// A cache hit should cost little more than the copy of its Header.
func TestBudgetResolveCached(t *testing.T) {
	y := New(Options{})
	now := okExpires.Add(-5 * time.Hour)
	y.now = func() time.Time { return now }
	s, err := okPlayer(t).stream(VisionOS, now)
	if err != nil {
		t.Fatal(err)
	}
	y.cache[okID] = s
	ctx := t.Context()
	// 3 allocs today.
	budget.Allocs(t, 4, 100, func() { sinkStream, sinkErr = y.Resolve(ctx, okID) })
}
