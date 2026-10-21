package account

import (
	"testing"
	"yt-music-player/internal/catalog"
)

// placeholderAccount is an Account over src that answers at once, so only
// the account's cost is measured; its changes go nowhere.
func placeholderAccount(src catalog.Source) (*Account, posts) {
	q := make(posts, 16)
	return New(src, func(fn func()) { q <- fn }, func(Change) {}), q
}

func placeholder() catalog.Source { return catalog.NewPlaceholderSource(0) }

// fetchRoundTrip is a fetch of who is signed in and its result.
func fetchRoundTrip(a *Account, q posts) {
	a.Fetch()
	(<-q)()
}

func BenchmarkFetch(b *testing.B) {
	a, q := placeholderAccount(placeholder())
	b.ReportAllocs()
	for b.Loop() {
		fetchRoundTrip(a, q)
	}
}

// likeRoundTrip is a Like and its answer, which goes through.
func likeRoundTrip(a *Account, q posts, id string, on bool) {
	a.Like(id, on, nil, func(error) {})
	(<-q)()
}

func BenchmarkLike(b *testing.B) {
	a, q := placeholderAccount(placeholder())
	id := catalog.Placeholder().Songs[0].ID
	on := true
	b.ReportAllocs()
	for b.Loop() {
		likeRoundTrip(a, q, id, on)
		on = !on
	}
}

// Sinks keep the compiler from optimizing measured calls away.
var (
	sinkStatus Status
	sinkBool   bool
)

// BenchmarkStatus is what `ytm status`, and a front-end connecting, cost
// the daemon's account.
func BenchmarkStatus(b *testing.B) {
	a, q := placeholderAccount(placeholder())
	fetchRoundTrip(a, q)
	b.ReportAllocs()
	for b.Loop() {
		sinkStatus, sinkBool = a.Status()
	}
}
