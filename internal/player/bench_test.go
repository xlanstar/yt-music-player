package player

import (
	"slices"
	"strconv"
	"testing"
)

var (
	sinkPlayer *Player
	sinkIDs    []string
	sinkString string
)

// bigQueue is the queue length of a large library played in full.
const bigQueue = 2000

func queueIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = "song-" + strconv.Itoa(i)
	}
	return ids
}

// bigPlayer is half-way through a queue of bigQueue songs.
func bigPlayer() *Player {
	p := New(queueIDs(bigQueue))
	p.at = bigQueue / 2
	return p
}

func BenchmarkNew(b *testing.B) {
	ids := queueIDs(bigQueue)
	b.ReportAllocs()
	for b.Loop() {
		sinkPlayer = New(ids)
	}
}

func BenchmarkCurrent(b *testing.B) {
	p := bigPlayer()
	b.ReportAllocs()
	for b.Loop() {
		sinkString = p.Current()
	}
}

var repeats = []struct {
	name   string
	repeat Repeat
}{{"off", RepeatOff}, {"all", RepeatAll}, {"one", RepeatOne}}

func BenchmarkNext(b *testing.B) {
	for _, r := range repeats {
		b.Run(r.name, func(b *testing.B) {
			p := bigPlayer()
			p.Repeat = r.repeat
			b.ReportAllocs()
			for b.Loop() {
				p.Next()
			}
		})
	}
}

// BenchmarkPrev skips RepeatOne, which Prev treats like RepeatAll.
func BenchmarkPrev(b *testing.B) {
	for _, r := range repeats[:2] {
		b.Run(r.name, func(b *testing.B) {
			p := bigPlayer()
			p.Repeat = r.repeat
			b.ReportAllocs()
			for b.Loop() {
				p.Prev()
			}
		})
	}
}

func BenchmarkFinished(b *testing.B) {
	for _, r := range repeats {
		b.Run(r.name, func(b *testing.B) {
			p := bigPlayer()
			p.Repeat = r.repeat
			b.ReportAllocs()
			for b.Loop() {
				p.Finished()
			}
		})
	}
}

func BenchmarkToggleShuffle(b *testing.B) {
	for _, tc := range []struct {
		name        string
		wasShuffled bool
	}{{"on", false}, {"off", true}} {
		b.Run(tc.name, func(b *testing.B) {
			p := bigPlayer()
			b.ReportAllocs()
			for b.Loop() {
				p.isShuffled = tc.wasShuffled
				p.ToggleShuffle()
			}
		})
	}
}

func BenchmarkPlay(b *testing.B) {
	b.Run("in_queue", func(b *testing.B) {
		p := bigPlayer()
		ids := [2]string{"song-10", "song-1990"}
		b.ReportAllocs()
		i := 0
		for b.Loop() {
			p.Play(ids[i%2])
			i++
		}
	})
	b.Run("not_in_queue", func(b *testing.B) {
		// The queue grows by a song each call; reset it now and then, off
		// the timer, so it stays about bigQueue long.
		fresh := queueIDs(100)
		for i := range fresh {
			fresh[i] = "new-" + fresh[i]
		}
		var p *Player
		b.ReportAllocs()
		i := 0
		for b.Loop() {
			if i%len(fresh) == 0 {
				b.StopTimer()
				p = bigPlayer()
				b.StartTimer()
			}
			p.Play(fresh[i%len(fresh)])
			i++
		}
	})
}

// playAllCases are lists of bigQueue songs played; their budgets live
// here.
var playAllCases = []struct {
	name    string
	shuffle bool
	ids     []string
	limit   float64
}{
	// 2 allocs today: the queue and its order.
	{"in_order", false, queueIDs(bigQueue), 2},
	// 3 allocs today: plus the shuffled order.
	{"shuffled", true, queueIDs(bigQueue), 3},
	// A list with each song twice, as a playlist may: 2 allocs today, as
	// in_order's.
	{"repeated", false, slices.Concat(queueIDs(bigQueue/2), queueIDs(bigQueue/2)), 2},
}

func BenchmarkPlayAll(b *testing.B) {
	for _, tc := range playAllCases {
		b.Run(tc.name, func(b *testing.B) {
			p := bigPlayer()
			p.isShuffled = tc.shuffle
			b.ReportAllocs()
			for b.Loop() {
				p.PlayAll(tc.ids)
			}
		})
	}
}

// BenchmarkAppendUpNext mirrors the queue panel: the next 20 songs into
// the slice it keeps.
func BenchmarkAppendUpNext(b *testing.B) {
	p := bigPlayer()
	dst := make([]string, 0, 20)
	b.ReportAllocs()
	for b.Loop() {
		dst = p.AppendUpNext(dst[:0], 20)
	}
	sinkIDs = dst
}

// BenchmarkAppendPlayOrder mirrors a daemon telling its queue: every song
// into a slice it keeps.
func BenchmarkAppendPlayOrder(b *testing.B) {
	p := bigPlayer()
	dst := make([]string, 0, bigQueue)
	b.ReportAllocs()
	for b.Loop() {
		dst = p.AppendPlayOrder(dst[:0])
	}
	sinkIDs = dst
}

// BenchmarkMove moves a song across the queue and back, around the
// current one.
func BenchmarkMove(b *testing.B) {
	for _, tc := range []struct {
		name    string
		shuffle bool
	}{{"in_order", false}, {"shuffled", true}} {
		b.Run(tc.name, func(b *testing.B) {
			p := bigPlayer()
			p.isShuffled = tc.shuffle
			b.ReportAllocs()
			for b.Loop() {
				p.Move(10, bigQueue-10)
				p.Move(bigQueue-10, 10)
			}
		})
	}
}

// enqueueCases enqueue a collection of 100 songs already in a long queue,
// past fewIDs, so the queue is indexed; their budgets live here.
var enqueueCases = []struct {
	name  string
	next  bool
	limit float64
}{
	// 9 allocs today: the queue's index.
	{"queued", false, 11},
	// 14 allocs today: plus the queued songs' indexes, which move.
	{"queued_next", true, 17},
}

func BenchmarkEnqueue(b *testing.B) {
	ids := queueIDs(100)
	for _, tc := range enqueueCases {
		b.Run(tc.name, func(b *testing.B) {
			p := bigPlayer()
			b.ReportAllocs()
			for b.Loop() {
				p.Enqueue(ids, tc.next)
			}
		})
	}
}

// BenchmarkEnqueueRemove adds a song after the current one and removes
// it, so the queue stays bigQueue long.
func BenchmarkEnqueueRemove(b *testing.B) {
	p := bigPlayer()
	ids := []string{"new"}
	b.ReportAllocs()
	for b.Loop() {
		p.Enqueue(ids, true)
		p.Remove(p.at + 1)
	}
}
