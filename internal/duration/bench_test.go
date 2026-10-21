package duration

import "testing"

// sinkString keeps the compiler from optimizing measured calls away.
var sinkString string

// clockCases are Clock's inputs; limit holds its allocations per call.
var clockCases = []struct {
	name    string
	seconds float64
	limit   float64
}{
	{"minutes", 227, 1},       // 1 alloc today.
	{"over_an_hour", 3725, 1}, // 1 alloc today.
}

func BenchmarkClock(b *testing.B) {
	for _, tc := range clockCases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkString = Clock(tc.seconds)
			}
		})
	}
}
