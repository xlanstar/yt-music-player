package auth

import (
	"net/http"
	"testing"
	"time"
	"yt-music-player/internal/budget"
)

func TestBudgetParse(t *testing.T) {
	for _, tc := range benchInputs {
		t.Run(tc.name, func(t *testing.T) {
			budget.Allocs(t, tc.limit, 100, func() { sinkCreds, sinkErr = Parse(tc.in) })
		})
	}
}

func TestBudgetDecorator(t *testing.T) {
	c, err := Parse("X-Goog-AuthUser: 1\nCookie: " + benchCookie)
	if err != nil {
		t.Fatal(err)
	}
	sign := c.Decorator(func() time.Time { return time.Unix(1_700_000_000, 0) })
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://music.youtube.com/youtubei/v1/browse", nil)
	if err != nil {
		t.Fatal(err)
	}
	consent := []string{"SOCS=CAI"}
	// 6 allocs today: the hash and the header values.
	budget.Allocs(t, 7, 100, func() {
		req.Header["Cookie"] = consent
		sinkErr = sign(req)
	})
}

func TestBudgetSAPISIDHash(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	// 1 alloc today: the string returned.
	budget.Allocs(t, 1, 100, func() { sinkString = SAPISIDHash("fakeSAPISID/abc", defaultOrigin, now) })
}

func TestBudgetMergeCookies(t *testing.T) {
	c, err := Parse(benchCookie)
	if err != nil {
		t.Fatal(err)
	}
	names := cookieNames(c.cookie)
	// 0 allocs today: the session has the transport's cookies already.
	budget.Allocs(t, 0, 100, func() { sinkString = mergeCookies(c.cookie, names, "SOCS=CAI") })
}

func TestBudgetMemoryStore(t *testing.T) {
	s := &MemoryStore{}
	c, err := Parse(benchCookie)
	if err != nil {
		t.Fatal(err)
	}
	// 0 allocs today, for both.
	budget.Allocs(t, 0, 100, func() { sinkErr = s.Save(c) })
	budget.Allocs(t, 0, 100, func() { sinkCreds, sinkErr = s.Load() })
}
