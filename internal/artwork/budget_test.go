package artwork

import (
	"net/http"
	"strconv"
	"testing"
	"yt-music-player/internal/budget"
)

func TestBudgetBucket(t *testing.T) {
	// 0 allocs today.
	budget.Allocs(t, 0, 100, func() { sinkInt = Bucket(336) })
}

func TestBudgetSizedURL(t *testing.T) {
	for _, tc := range sizedURLs {
		t.Run(tc.name, func(t *testing.T) {
			budget.Allocs(t, tc.limit, 100, func() { sinkString = SizedURL(tc.url, coverPx) })
		})
	}
}

func TestBudgetDecode(t *testing.T) {
	for _, tc := range decodeCases {
		t.Run(tc.name, func(t *testing.T) {
			data := benchFile()[tc.name]
			// Each takes milliseconds and allocates the same each time.
			budget.Within(t, tc.limit, 3, func() { sinkImage, sinkErr = Decode(data, coverPx) })
		})
	}
}

func TestBudgetShrink(t *testing.T) {
	for _, tc := range shrinkCases {
		t.Run(strconv.Itoa(tc.n), func(t *testing.T) {
			m := benchCover(tc.n)
			budget.Within(t, tc.limit, 5, func() { sinkRGBA = shrink(m, coverPx) })
		})
	}
}

func TestBudgetAverage(t *testing.T) {
	m := benchCover(coverPx)
	// 0 allocs today.
	budget.Allocs(t, 0, 20, func() { sinkColor = average(m) })
}

func TestBudgetTrimBars(t *testing.T) {
	for _, tc := range trimCases() {
		t.Run(tc.name, func(t *testing.T) {
			budget.Allocs(t, tc.limit, 20, func() { sinkRect = trimBars(tc.m) })
		})
	}
}

func TestBudgetGet(t *testing.T) {
	for _, tc := range getCases {
		t.Run(tc.name, func(t *testing.T) {
			l := heldLoader(t, 24, defaultMemoryBytes)
			u := tc.setup(l)
			// 0 allocs today, for each: Get runs for every cover of every frame.
			budget.Allocs(t, 0, 100, func() { sinkImage = l.Get(u, coverPx) })
		})
	}
}

func TestBudgetNextFrame(t *testing.T) {
	for _, tc := range frameCases {
		t.Run(tc.name, func(t *testing.T) {
			l := heldLoader(t, tc.n, tc.memory)
			urls := make([]string, tc.n)
			for i := range urls {
				urls[i] = coverURL(i)
			}
			// 0 allocs today.
			budget.Allocs(t, 0, 20, func() {
				l.NextFrame()
				for _, u := range urls {
					sinkImage = l.Get(u, coverPx)
				}
			})
		})
	}
}

func TestBudgetFinish(t *testing.T) {
	// 3 allocs today: the entry, and the URL's sizes.
	budget.Allocs(t, 4, 100, evictingLoader(t))
}

func TestBudgetFetch(t *testing.T) {
	u := coverURL(0)
	t.Run("disk", func(t *testing.T) {
		l := offlineLoader(t, 0)
		l.disk = warmDisk(t, u)
		// 9 allocs today: the file's name and its bytes.
		budget.Allocs(t, 11, 20, func() { sinkBytes, sinkErr = l.fetch(u) })
	})
	t.Run("shared_download", func(t *testing.T) {
		l := sharingLoader(t, u)
		// 0 allocs today.
		budget.Allocs(t, 0, 100, func() { sinkBytes, sinkErr = l.fetch(u) })
	})
}

func TestBudgetLoad(t *testing.T) {
	data := benchFile()["jpeg/544"]
	l := New(Options{Client: &http.Client{Transport: memoryTransport(data)}})
	t.Cleanup(l.Close)
	k := key{coverURL(0), coverPx}
	// 35 allocs, 2.27 MB today, most of them the decoder's: the body is
	// read into one buffer of its Content-Length.
	budget.Within(t, budget.Cost{Allocs: 40, Bytes: 2.61e6}, 5, func() { sinkImage, sinkErr = l.load(k) })
}
