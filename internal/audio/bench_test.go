package audio

import "testing"

var (
	sinkFloat float64
	sinkBool  bool
	sinkEvent Event
)

// AVPlayer is only measured live (live_darwin_test.go); here, the Fake
// as the app drives it and the arithmetic around the system's player.

var benchStream = Media{URL: "https://rr1---sn-a5mekn6s.googlevideo.com/videoplayback?itag=140", Seconds: 213.1}

type fakeCall struct {
	name  string
	limit float64
	call  func()
}

// fakeCalls are what playback does to an engine per song. App frame tests
// drive the Fake, which must not count against them: Load allocates 2
// today (its note), the others 0.
func fakeCalls(f *Fake) []fakeCall {
	return []fakeCall{
		{"Load", 2, func() { f.Load(benchStream, 84) }},
		{"PlayPause", 0, func() { f.Play(); f.Pause() }},
		{"Position", 0, func() { sinkFloat = f.Position() }},
		{"SetInfo", 0, func() { f.SetInfo(Info{Title: "Midnight Arcade", Artist: "Lumen Drive", Seconds: 227}) }},
		{"Emit", 0, func() { f.Emit(Ready) }},
	}
}

func BenchmarkFake(b *testing.B) {
	f := NewFake(func(e Event) { sinkEvent = e })
	for _, tc := range fakeCalls(f) {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				tc.call()
				f.Calls = f.Calls[:0]
			}
		})
	}
}

func BenchmarkIsCommand(b *testing.B) {
	e := Event{Kind: CommandNext}
	b.ReportAllocs()
	for b.Loop() {
		sinkBool = e.IsCommand()
	}
}

func BenchmarkHalvedEnd(b *testing.B) {
	for _, tc := range []struct {
		name             string
		duration, length float64
	}{{"doubled", 426.2, 213.1}, {"right", 213.1, 213.1}, {"unknown", 426.2, 0}} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkFloat = halvedEnd(tc.duration, tc.length)
			}
		})
	}
}
