package fetchstate

import (
	"strconv"
	"testing"
)

// Sinks keep the compiler from optimizing measured calls away.
var sinkFetch Fetch

// benchRegistry is a registry after a long session: n playlists loaded
// beside the feeds and account resources.
func benchRegistry(n int) *Registry {
	var r Registry
	for _, res := range []Resource{{Kind: Home}, {Kind: Explore}, {Kind: Account}, {Kind: Library}, {Kind: Liked}} {
		r.Set(res, Fetch{State: Loaded})
	}
	for i := range n {
		r.Set(Resource{Kind: Playlist, ID: "pl-" + strconv.Itoa(i)}, Fetch{State: Loaded})
	}
	return &r
}

var (
	benchLoaded = Resource{Kind: Playlist, ID: "pl-7"}
	benchIdle   = Resource{Kind: Album, ID: "never-shown"}
)

// BenchmarkState is read by pages in every build.
func BenchmarkState(b *testing.B) {
	for _, n := range []int{20, 2000} {
		r := benchRegistry(n)
		b.Run("loaded/"+strconv.Itoa(n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkFetch = r.State(benchLoaded)
			}
		})
		b.Run("idle/"+strconv.Itoa(n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkFetch = r.State(benchIdle)
			}
		})
	}
}

// BenchmarkSet records a fetch as it starts and as it ends.
func BenchmarkSet(b *testing.B) {
	r := benchRegistry(20)
	b.ReportAllocs()
	for b.Loop() {
		r.Set(benchLoaded, Fetch{State: Loading})
		r.Set(benchLoaded, Fetch{State: Loaded})
	}
}
