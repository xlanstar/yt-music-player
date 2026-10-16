package audio

import (
	"net/http"
	"testing"
	"yt-music-player/internal/budget"
)

func TestBudgetFake(t *testing.T) {
	f := NewFake(func(e Event) { sinkEvent = e })
	for _, tc := range fakeCalls(f) {
		t.Run(tc.name, func(t *testing.T) {
			budget.Allocs(t, tc.limit, 100, func() {
				tc.call()
				f.Calls = f.Calls[:0]
			})
		})
	}
}

func TestBudgetSourceRead(t *testing.T) {
	for _, tc := range sourceCases {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: memTransport{testBytes(tc.size)}}
			buf := make([]byte, 16<<10)
			budget.Within(t, tc.limit, 10, func() { readSource(t, client, buf) })
		})
	}
}

func TestBudgetOpusRead(t *testing.T) {
	s := openTone(t, toneFixtures[1])
	buf := make([]float32, 2048*2)
	// 8 allocs today for six seconds, 5 of them SetPosition's.
	budget.Allocs(t, 10, 20, func() { decodeThrough(t, s, buf) })
}

func TestBudgetOpusSetPosition(t *testing.T) {
	s := openTone(t, toneFixtures[1])
	// 5 allocs today: the Opus decoder's SILK state, made anew.
	budget.Allocs(t, 6, 100, func() { sinkErr = s.SetPosition(3 * opusRate) })
}
