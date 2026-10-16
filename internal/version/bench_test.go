package version

import "testing"

// sinkBool keeps the compiler from optimizing measured calls away.
var sinkBool bool

func BenchmarkOlder(b *testing.B) {
	for _, tc := range olderCases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkBool = Older(tc.a, tc.b)
			}
		})
	}
}

// olderCases are what a Hello compares; a front-end does it once a
// connection. limit holds allocations per call.
var olderCases = []struct {
	name, a, b string
	limit      float64
}{
	{"release", "0.9.12", "0.10.0", 2},             // 2 allocs today: each version's Split.
	{"pre-release", "1.0.0-rc.1", "1.0.0-rc.2", 5}, // 4 allocs today: each version's two Splits.
}
