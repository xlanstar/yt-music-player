package playback

import (
	"testing"
	"yt-music-player/internal/notice"
	"yt-music-player/internal/player"
)

var (
	sinkFloat  float64
	sinkBool   bool
	sinkNotice notice.Notice
)

// followCases are the states the daemon's batches find the player in:
// playing (reads the engine) and paused.
var followCases = []struct {
	name  string
	ready func(x *fixture)
}{
	{"playing", func(*fixture) {}},
	{"paused", func(x *fixture) { x.player.Playing = false }},
}

// playing is a fixture 30.5 s into a song on the fake engine.
func playing(tb testing.TB) *fixture {
	tb.Helper()
	x := newFixture(&instantResolver{})
	x.startPlaying(tb)
	x.f.Pos = 30.5
	tb.Cleanup(x.pb.Stop)
	return x
}

func BenchmarkFollow(b *testing.B) {
	for _, tc := range followCases {
		b.Run(tc.name, func(b *testing.B) {
			x := playing(b)
			b.ReportAllocs()
			for b.Loop() {
				tc.ready(x)
				x.pb.Follow()
			}
		})
	}
}

// idleCases are what the daemon asks of playback every batch, besides
// Follow.
func idleCases(x *fixture) []struct {
	name string
	call func()
} {
	song, _ := x.cat.Song(x.player.Current())
	return []struct {
		name string
		call func()
	}{
		{"SongLength", func() { sinkFloat = x.pb.SongLength(song) }},
		{"Waiting", func() { sinkBool = x.pb.IsWaiting() }},
		{"TakeNotice", func() { sinkNotice, sinkBool = x.pb.TakeNotice() }},
	}
}

func BenchmarkIdle(b *testing.B) {
	x := playing(b)
	for _, tc := range idleCases(x) {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				tc.call()
			}
		})
	}
}

// nextSong skips to the next song and loads it, resolved or prefetched.
// The player must repeat the queue, else it stops at the end.
func nextSong(x *fixture, prefetched bool) {
	x.player.Next()
	if prefetched {
		x.pb.next = prefetch{song: x.player.Current(), stream: x.pb.stream, isResolved: true}
	}
	x.pb.Follow()
	if !prefetched {
		(<-x.q)()
	}
	// The fake keeps its calls, which would grow.
	x.f.Calls = x.f.Calls[:0]
}

func BenchmarkNext(b *testing.B) {
	for _, prefetched := range []bool{false, true} {
		name := "resolved"
		if prefetched {
			name = "prefetched"
		}
		b.Run(name, func(b *testing.B) {
			x := playing(b)
			x.player.Repeat = player.RepeatAll
			b.ReportAllocs()
			for b.Loop() {
				nextSong(x, prefetched)
			}
		})
	}
}
