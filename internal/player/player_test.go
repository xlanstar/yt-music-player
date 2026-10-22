package player

import (
	"slices"
	"testing"
)

var abcd = []string{"a", "b", "c", "d"}

func TestPlayTogglesCurrentAndStartsOthers(t *testing.T) {
	p := New(abcd)
	if p.Current() != "a" || p.isPlaying {
		t.Fatalf("new player at %q playing %v", p.Current(), p.isPlaying)
	}
	p.Play("a")
	if !p.isPlaying {
		t.Error("playing the current song did not resume it")
	}
	p.Play("a")
	if p.isPlaying {
		t.Error("playing the current song again did not pause it")
	}
	p.position = 40
	p.Play("c")
	if p.Current() != "c" || !p.isPlaying || p.position != 0 {
		t.Errorf("at %q playing %v position %v", p.Current(), p.isPlaying, p.position)
	}
}

func TestPlayOutsideQueueQueuesNext(t *testing.T) {
	p := New(abcd)
	p.Play("b")
	p.Play("x")
	if p.Current() != "x" {
		t.Fatalf("current %q", p.Current())
	}
	if got := p.AppendUpNext(nil, 5); !slices.Equal(got, []string{"c", "d"}) {
		t.Errorf("up next %q", got)
	}
	p.Prev()
	if p.Current() != "b" {
		t.Errorf("before x is %q, want b", p.Current())
	}

	empty := New(nil)
	empty.Play("x")
	if empty.Current() != "x" || !empty.isPlaying {
		t.Errorf("empty queue: current %q playing %v", empty.Current(), empty.isPlaying)
	}
}

func TestNextWrapsAndPlaysOn(t *testing.T) {
	p := New(abcd)
	p.Play("d")
	p.Next()
	if p.Current() != "a" || !p.isPlaying {
		t.Errorf("past the end without repeat: at %q playing %v", p.Current(), p.isPlaying)
	}
	one := New([]string{"a"})
	one.Play("a")
	one.position = 42
	one.Next()
	if one.Current() != "a" || !one.isPlaying || one.position != 0 {
		t.Errorf("one song: at %q playing %v position %v", one.Current(), one.isPlaying, one.position)
	}
}

func TestNextAndPrevKeepPaused(t *testing.T) {
	p := New(abcd)
	p.Play("b")
	p.Pause()
	for _, step := range []struct {
		name string
		do   func()
		want string
	}{
		{"next", p.Next, "c"},
		{"prev", p.Prev, "b"},
		{"next past the end", func() { p.Play("d"); p.Pause(); p.Next() }, "a"},
		{"prev restarting", func() { p.position = restartAfter + 1; p.Prev() }, "a"},
	} {
		step.do()
		if p.Current() != step.want || p.isPlaying {
			t.Errorf("%s: at %q playing %v, want %q paused", step.name, p.Current(), p.isPlaying, step.want)
		}
	}
}

func TestSkipWrapsAndStopsUnlessRepeating(t *testing.T) {
	p := New(abcd)
	p.Play("d")
	p.Skip()
	if p.Current() != "a" || p.isPlaying {
		t.Errorf("past the end without repeat: at %q playing %v", p.Current(), p.isPlaying)
	}
	p.repeat = RepeatAll
	p.Play("d")
	p.Skip()
	if p.Current() != "a" || !p.isPlaying {
		t.Errorf("past the end repeating: at %q playing %v", p.Current(), p.isPlaying)
	}
	p.repeat = RepeatOff
	p.Play("d")
	p.Finished()
	if p.Current() != "a" || p.isPlaying {
		t.Errorf("finished past the end: at %q playing %v", p.Current(), p.isPlaying)
	}
}

func TestPrevRestartsSongUnderWay(t *testing.T) {
	p := New(abcd)
	p.Play("c")
	p.position = restartAfter + 1
	p.Prev()
	if p.Current() != "c" || p.position != 0 {
		t.Fatalf("at %q position %v, want c restarted", p.Current(), p.position)
	}
	p.Prev()
	if p.Current() != "b" {
		t.Errorf("at %q, want b", p.Current())
	}
	p.Play("a")
	p.Prev()
	if p.Current() != "a" {
		t.Errorf("before the first song without repeat went to %q", p.Current())
	}
	p.repeat = RepeatAll
	p.Prev()
	if p.Current() != "d" {
		t.Errorf("before the first song repeating went to %q", p.Current())
	}
}

func TestFinishedHonorsRepeatOne(t *testing.T) {
	p := New(abcd)
	p.repeat = RepeatOne
	p.position = 100
	p.Finished()
	if p.Current() != "a" || p.position != 0 {
		t.Errorf("repeat one: at %q position %v", p.Current(), p.position)
	}
	p.Next()
	if p.Current() != "b" {
		t.Errorf("a skip under repeat one stayed at %q", p.Current())
	}
	p.repeat = RepeatOff
	p.Finished()
	if p.Current() != "c" {
		t.Errorf("finished without repeat went to %q", p.Current())
	}
}

func TestUpNext(t *testing.T) {
	p := New(abcd)
	p.Play("c")
	if got := p.AppendUpNext(nil, 8); !slices.Equal(got, []string{"d"}) {
		t.Errorf("without repeat %q", got)
	}
	p.repeat = RepeatAll
	if got := p.AppendUpNext(nil, 8); !slices.Equal(got, []string{"d", "a", "b"}) {
		t.Errorf("repeating %q", got)
	}
	if got := p.AppendUpNext(nil, 2); !slices.Equal(got, []string{"d", "a"}) {
		t.Errorf("limited %q", got)
	}
	if got := New(nil).AppendUpNext(nil, 3); got != nil {
		t.Errorf("empty queue %q", got)
	}
	buf := make([]string, 0, 8)
	if got := p.AppendUpNext(append(buf, "x"), 2); !slices.Equal(got, []string{"x", "d", "a"}) {
		t.Errorf("appended %q", got)
	}
	if n := testing.AllocsPerRun(100, func() { buf = p.AppendUpNext(buf[:0], 8) }); n != 0 {
		t.Errorf("AppendUpNext allocates %v times with room", n)
	}
}

func TestShuffleKeepsCurrentAndEverySong(t *testing.T) {
	p := New(abcd)
	p.Play("b")
	p.ToggleShuffle()
	if !p.IsShuffled() || p.Current() != "b" {
		t.Fatalf("shuffled %v at %q", p.IsShuffled(), p.Current())
	}
	next := p.AppendUpNext(nil, 8)
	sorted := slices.Sorted(slices.Values(next))
	if !slices.Equal(sorted, []string{"a", "c", "d"}) {
		t.Errorf("shuffled up next %q lost or repeated songs", next)
	}
	p.Next()
	if p.Current() != next[0] {
		t.Errorf("next went to %q, up next said %q", p.Current(), next[0])
	}
	cur := p.Current()
	p.ToggleShuffle()
	if p.IsShuffled() || p.Current() != cur {
		t.Errorf("unshuffled %v at %q, want %q", p.IsShuffled(), p.Current(), cur)
	}
	want := abcd[slices.Index(abcd, cur)+1:]
	if got := p.AppendUpNext(nil, 8); !slices.Equal(got, want) {
		t.Errorf("unshuffled up next %q, want %q", got, want)
	}
}

func TestCycleRepeat(t *testing.T) {
	p := New(abcd)
	for _, want := range []Repeat{RepeatAll, RepeatOne, RepeatOff} {
		p.CycleRepeat()
		if p.repeat != want {
			t.Fatalf("repeat %d, want %d", p.repeat, want)
		}
	}
}

func TestToggleMute(t *testing.T) {
	p := New(abcd)
	p.ToggleMute()
	if !p.isMuted || !p.IsSilent() {
		t.Fatal("not muted")
	}
	p.ToggleMute()
	if p.isMuted || p.volume != defaultVolume {
		t.Fatalf("unmuted %v at volume %v", !p.isMuted, p.volume)
	}
	p.volume = 0
	if !p.IsSilent() {
		t.Fatal("zero volume is not silent")
	}
	p.ToggleMute()
	if p.IsSilent() || p.volume != unmuteVolume {
		t.Errorf("unmuting at zero volume left volume %v", p.volume)
	}
}

func TestPlayAllReplacesTheQueue(t *testing.T) {
	p := New(abcd)
	p.Play("c")
	p.position = 30
	p.PlayAll([]string{"x", "y", "z"}, 0)
	if p.Current() != "x" || !p.isPlaying || p.position != 0 {
		t.Fatalf("at %q playing %v position %v", p.Current(), p.isPlaying, p.position)
	}
	if got := p.AppendUpNext(nil, 5); !slices.Equal(got, []string{"y", "z"}) {
		t.Errorf("up next %q", got)
	}

	p.ToggleShuffle()
	p.PlayAll(abcd, 0)
	if p.Current() != "a" || !p.IsShuffled() {
		t.Fatalf("shuffled play all at %q, shuffled %v", p.Current(), p.IsShuffled())
	}
	rest := p.AppendUpNext(nil, 5)
	slices.Sort(rest)
	if !slices.Equal(rest, []string{"b", "c", "d"}) {
		t.Errorf("shuffled up next %q", rest)
	}

	p.PlayAll(nil, 0)
	if p.Current() != "a" {
		t.Errorf("playing nothing moved to %q", p.Current())
	}
}

func TestLen(t *testing.T) {
	p := New(abcd)
	p.Play("x")
	if p.Len() != 5 {
		t.Errorf("len %d after queueing x, want 5", p.Len())
	}
	if New(nil).Len() != 0 {
		t.Error("an empty queue is not empty")
	}
}

// The modes' values are daemon.json's: changing one misreads saved files.
func TestRepeatValues(t *testing.T) {
	for _, tc := range []struct{ mode, value Repeat }{{RepeatOff, 0}, {RepeatAll, 1}, {RepeatOne, 2}} {
		if tc.mode != tc.value || !tc.mode.Valid() {
			t.Errorf("mode %d, want %d and valid", tc.mode, tc.value)
		}
	}
	if repeatModes.Valid() || Repeat(255).Valid() {
		t.Error("a value past the modes is valid")
	}
}
