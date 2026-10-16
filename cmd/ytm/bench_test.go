package main

import (
	"io"
	"testing"
	"yt-music-player/internal/api"
)

// Status bars poll `ytm status --json` every second or so: rendering it
// is the one path a command takes often.

// sinkString keeps the compiler from optimizing measured calls away.
var sinkString string

// statusCases are the statuses rendered; limit holds allocations per
// call of each benchmark.
var statusCases = []struct {
	name             string
	status           api.Status
	jsonLimit, limit float64
}{
	{"playing", playingStatus(), 6, 79},                      // 5 and 68 allocs today.
	{"idle", api.Status{State: api.State{Volume: 1}}, 4, 15}, // 3 and 13 allocs today.
}

func BenchmarkStatusJSON(b *testing.B) {
	for _, tc := range statusCases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = writeJSON(io.Discard, statusOf(&tc.status))
			}
		})
	}
}

func BenchmarkStatusText(b *testing.B) {
	for _, tc := range statusCases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkString = statusText(&tc.status)
			}
		})
	}
}
