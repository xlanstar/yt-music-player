package player

import (
	"math"
	"slices"
	"testing"
)

// playOrder is the queue as a front-end shows it.
func playOrder(p *Player) []string { return p.AppendPlayOrder(nil) }

// checkQueue fails t unless p plays want, at current.
func checkQueue(t *testing.T, p *Player, want []string, current string) {
	t.Helper()
	if got := playOrder(p); !slices.Equal(got, want) {
		t.Errorf("play order %q, want %q", got, want)
	}
	if p.Current() != current {
		t.Errorf("current %q, want %q", p.Current(), current)
	}
	if p.Current() != "" && playOrder(p)[p.Index()] != p.Current() {
		t.Errorf("index %d is not the current song's", p.Index())
	}
}

func TestPlayFrom(t *testing.T) {
	p := New(nil)
	p.PlayFrom(abcd, 2)
	checkQueue(t, p, abcd, "c")
	if !p.Playing || p.Position != 0 {
		t.Errorf("playing %v at %v", p.Playing, p.Position)
	}
	p.PlayFrom(abcd, 9)
	checkQueue(t, p, abcd, "d")

	p.ToggleShuffle()
	p.PlayFrom(abcd, 1)
	if got := playOrder(p); got[0] != "b" || p.Index() != 0 {
		t.Errorf("shuffled from b: %q at %d", got, p.Index())
	}
}

func TestEnqueue(t *testing.T) {
	p := New(abcd)
	p.Play("b")
	gen := p.QueueGen()
	p.Enqueue([]string{"x", "y"}, true)
	checkQueue(t, p, []string{"a", "b", "x", "y", "c", "d"}, "b")
	p.Enqueue([]string{"z"}, false)
	checkQueue(t, p, []string{"a", "b", "x", "y", "c", "d", "z"}, "b")
	if p.QueueGen() == gen {
		t.Error("enqueueing kept the queue's gen")
	}
	// Unshuffled, the queue itself holds the new songs where they play, so
	// a shuffle undone keeps them there.
	p.ToggleShuffle()
	p.ToggleShuffle()
	checkQueue(t, p, []string{"a", "b", "x", "y", "c", "d", "z"}, "b")

	empty := New(nil)
	empty.Enqueue([]string{"x", "y"}, true)
	checkQueue(t, empty, []string{"x", "y"}, "x")
	if empty.Playing {
		t.Error("enqueueing started playing")
	}
	empty.Enqueue(nil, false)
	checkQueue(t, empty, []string{"x", "y"}, "x")
}

func TestEnqueueShuffled(t *testing.T) {
	p := New(abcd)
	p.ToggleShuffle()
	p.Next()
	current := p.Current()
	p.Enqueue([]string{"x"}, true)
	p.Enqueue([]string{"y"}, false)
	got := playOrder(p)
	if got[p.Index()+1] != "x" || got[len(got)-1] != "y" || p.Current() != current {
		t.Errorf("play order %q at %d, current %q", got, p.Index(), p.Current())
	}
	if !isPermutation(p.order, len(p.queue)) {
		t.Errorf("order %v is not one of %d songs", p.order, len(p.queue))
	}
}

func TestRemove(t *testing.T) {
	p := New(abcd)
	p.Play("c")
	p.Position = 30
	p.Remove(0)
	checkQueue(t, p, []string{"b", "c", "d"}, "c")
	if p.Position != 30 {
		t.Error("removing another song moved the current one")
	}
	p.Remove(2)
	checkQueue(t, p, []string{"b", "c"}, "c")
	// The current song removed, the next one plays from its start; past
	// the end it wraps and stops.
	p.Remove(1)
	checkQueue(t, p, []string{"b"}, "b")
	if p.Playing || p.Position != 0 {
		t.Errorf("removed the last song: playing %v at %v", p.Playing, p.Position)
	}
	p.Remove(5)
	p.Remove(-1)
	checkQueue(t, p, []string{"b"}, "b")
	p.Remove(0)
	checkQueue(t, p, nil, "")

	shuffled := New(abcd)
	shuffled.ToggleShuffle()
	order := playOrder(shuffled)
	shuffled.Remove(2)
	checkQueue(t, shuffled, slices.Delete(order, 2, 3), order[0])
}

func TestMove(t *testing.T) {
	p := New(abcd)
	p.Play("b")
	p.Move(0, 3)
	checkQueue(t, p, []string{"b", "c", "d", "a"}, "b")
	p.Move(0, 2)
	checkQueue(t, p, []string{"c", "d", "b", "a"}, "b")
	p.Move(3, 0)
	checkQueue(t, p, []string{"a", "c", "d", "b"}, "b")
	p.Move(1, 1)
	p.Move(1, 9)
	checkQueue(t, p, []string{"a", "c", "d", "b"}, "b")
	// Unshuffled, the queue changes, so a shuffle undone keeps the move.
	p.ToggleShuffle()
	p.ToggleShuffle()
	checkQueue(t, p, []string{"a", "c", "d", "b"}, "b")

	p.ToggleShuffle()
	order := playOrder(p)
	p.Move(3, 1)
	move(order, 3, 1)
	checkQueue(t, p, order, "b")
}

func TestClearUpNext(t *testing.T) {
	p := New(abcd)
	p.Play("b")
	p.ClearUpNext()
	checkQueue(t, p, []string{"a", "b"}, "b")
	p.ClearUpNext()
	checkQueue(t, p, []string{"a", "b"}, "b")

	shuffled := New(abcd)
	shuffled.ToggleShuffle()
	shuffled.Next()
	order := playOrder(shuffled)
	shuffled.ClearUpNext()
	checkQueue(t, shuffled, order[:2], order[1])
}

func TestClear(t *testing.T) {
	p := New(abcd)
	p.Play("b")
	p.Clear()
	checkQueue(t, p, nil, "")
	if p.Playing || p.Position != 0 || p.Len() != 0 {
		t.Errorf("cleared: playing %v at %v, %d songs", p.Playing, p.Position, p.Len())
	}
	p.Enqueue([]string{"x"}, false)
	checkQueue(t, p, []string{"x"}, "x")
}

func TestRestore(t *testing.T) {
	p := New(abcd)
	p.ToggleShuffle()
	p.Next()
	r := Restore(p.Queued(), p.Order(), p.Index(), p.IsShuffled())
	checkQueue(t, r, playOrder(p), p.Current())
	if r.Playing || !r.IsShuffled() {
		t.Errorf("restored playing %v, shuffled %v", r.Playing, r.IsShuffled())
	}
	for _, order := range [][]int{{0, 1}, {0, 1, 2, 2}, {0, 1, 2, 4}, nil} {
		bad := Restore(abcd, order, 9, true)
		checkQueue(t, bad, abcd, "d")
		if bad.IsShuffled() {
			t.Errorf("order %v restored shuffled", order)
		}
	}
	checkQueue(t, Restore(nil, nil, 3, false), nil, "")
}

func TestPauseResumeSeekVolume(t *testing.T) {
	p := New(nil)
	p.Resume()
	p.Seek(30)
	if p.Playing || p.Position != 0 {
		t.Errorf("empty queue: playing %v at %v", p.Playing, p.Position)
	}
	p = New(abcd)
	p.Resume()
	if !p.Playing {
		t.Error("not resumed")
	}
	p.Pause()
	if p.Playing {
		t.Error("not paused")
	}
	p.Seek(-3)
	if p.Position != 0 {
		t.Errorf("sought before the start: at %v", p.Position)
	}
	p.Seek(42)
	p.Seek(math.NaN())
	if p.Position != 42 {
		t.Errorf("at %v, want 42", p.Position)
	}
	p.Muted = true
	p.SetVolume(1.5)
	if p.Volume != 1 || p.Muted {
		t.Errorf("volume %v, muted %v", p.Volume, p.Muted)
	}
	p.SetVolume(math.NaN())
	p.SetVolume(-1)
	if p.Volume != 0 {
		t.Errorf("volume %v, want 0", p.Volume)
	}
}

func TestQueueGen(t *testing.T) {
	p := New(abcd)
	gen := p.QueueGen()
	p.Next()
	p.Play("c")
	p.Position = 10
	p.CycleRepeat()
	if p.QueueGen() != gen {
		t.Error("moving through the queue changed its gen")
	}
	for _, tc := range []struct {
		name   string
		change func()
	}{
		{"shuffle", p.ToggleShuffle},
		{"play new", func() { p.Play("x") }},
		{"play all", func() { p.PlayAll(abcd) }},
		{"enqueue", func() { p.Enqueue([]string{"y"}, true) }},
		{"remove", func() { p.Remove(0) }},
		{"move", func() { p.Move(0, 1) }},
		{"clear up next", p.ClearUpNext},
		{"clear", p.Clear},
	} {
		gen := p.QueueGen()
		tc.change()
		if p.QueueGen() == gen {
			t.Errorf("%s kept the queue's gen", tc.name)
		}
	}
}
