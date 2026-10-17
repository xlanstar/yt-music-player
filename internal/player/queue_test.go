package player

import (
	"math"
	"math/rand/v2"
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

// checkOnce fails t unless p's queue holds each song once and its play
// order is one of the queue's.
func checkOnce(t *testing.T, p *Player) {
	t.Helper()
	seen := map[string]bool{}
	for _, id := range playOrder(p) {
		if seen[id] {
			t.Errorf("%q queued twice: %q", id, playOrder(p))
		}
		seen[id] = true
	}
	if !isPermutation(p.order, len(p.queue)) {
		t.Errorf("order %v is not one of %d songs", p.order, len(p.queue))
	}
	if p.Len() > 0 && (p.Index() < 0 || p.Index() >= p.Len()) {
		t.Errorf("index %d of %d songs", p.Index(), p.Len())
	}
}

// many is more IDs than Enqueue looks for one by one.
func many(prefix string) []string {
	ids := make([]string, fewIDs+4)
	for i := range ids {
		ids[i] = prefix + string(rune('A'+i))
	}
	return ids
}

func TestEnqueueQueuedSkipped(t *testing.T) {
	for _, shuffle := range []bool{false, true} {
		p := New(abcd)
		if shuffle {
			p.ToggleShuffle()
		}
		p.Play("b")
		before := playOrder(p)
		p.Enqueue([]string{"c", "x", "b", "x", "a", "y"}, false)
		checkQueue(t, p, append(before, "x", "y"), "b")
		checkOnce(t, p)

		gen := p.QueueGen()
		p.Enqueue([]string{"a", "x", "b"}, false)
		if p.QueueGen() != gen {
			t.Errorf("shuffle %v: enqueueing queued songs changed the queue", shuffle)
		}

		// Past fewIDs, the queue is indexed instead.
		ids := many("m")
		before = playOrder(p)
		p.Enqueue(slices.Concat([]string{"a", "x"}, ids, ids[:3]), false)
		checkQueue(t, p, append(before, ids...), "b")
		checkOnce(t, p)
	}
}

func TestEnqueueNextMovesQueued(t *testing.T) {
	p := New([]string{"a", "b", "c", "d", "e"})
	p.Play("b")
	p.Position = 30
	p.Enqueue([]string{"d", "b", "x", "d", "a"}, true)
	checkQueue(t, p, []string{"b", "d", "x", "a", "c", "e"}, "b")
	checkOnce(t, p)
	if p.Position != 30 || !p.Playing {
		t.Errorf("playing %v at %v", p.Playing, p.Position)
	}
	// Unshuffled, the queue moved too, so a shuffle undone keeps it.
	p.ToggleShuffle()
	p.ToggleShuffle()
	checkQueue(t, p, []string{"b", "d", "x", "a", "c", "e"}, "b")

	ids := many("m")
	p.Enqueue(slices.Concat([]string{"c", "b"}, ids, []string{"c", "a"}), true)
	checkQueue(t, p, slices.Concat([]string{"b", "c"}, ids, []string{"a", "d", "x", "e"}), "b")
	checkOnce(t, p)
}

func TestEnqueueNextMovesQueuedShuffled(t *testing.T) {
	for _, ids := range [][]string{{"d", "b", "x", "d", "a"}, slices.Concat([]string{"d", "b"}, many("m"), []string{"a", "d"})} {
		p := New([]string{"a", "b", "c", "d", "e"})
		p.ToggleShuffle()
		p.Next()
		p.Next()
		current := p.Current()
		p.Enqueue(ids, true)
		checkOnce(t, p)
		var want []string
		for j, id := range ids {
			if id != current && !slices.Contains(ids[:j], id) {
				want = append(want, id)
			}
		}
		got := playOrder(p)
		if p.Current() != current || !slices.Equal(got[p.Index()+1:p.Index()+1+len(want)], want) {
			t.Errorf("play order %q at %d, current %q, want %q next after %q", got, p.Index(), p.Current(), want, current)
		}
		if all := slices.Compact(slices.Sorted(slices.Values(slices.Concat(abcd, []string{"e"}, ids)))); p.Len() != len(all) {
			t.Errorf("%d songs, want %d", p.Len(), len(all))
		}
	}
}

func TestPlayFromRepeats(t *testing.T) {
	ids := []string{"a", "b", "a", "c", "b", "d"}
	p := New(nil)
	p.PlayFrom(ids, 4)
	checkQueue(t, p, abcd, "b")
	checkOnce(t, p)
	p.PlayFrom(ids, 2)
	checkQueue(t, p, abcd, "a")
	if ids[1] != "b" || len(ids) != 6 {
		t.Errorf("PlayFrom changed its argument: %q", ids)
	}

	p.ToggleShuffle()
	p.PlayFrom(ids, 4)
	checkOnce(t, p)
	if got := playOrder(p); got[0] != "b" || p.Index() != 0 || len(got) != 4 {
		t.Errorf("shuffled from b: %q at %d", got, p.Index())
	}

	checkQueue(t, New([]string{"a", "a", "b", "a"}), []string{"a", "b"}, "a")
}

func TestRestoreRepeats(t *testing.T) {
	queue := []string{"a", "b", "a", "c"}
	for _, tc := range []struct {
		name     string
		order    []int
		index    int
		shuffled bool
		want     []string
		current  string
	}{
		{"in order", nil, 2, false, []string{"a", "b", "c"}, "a"},
		{"in order, after a repeat", nil, 3, false, []string{"a", "b", "c"}, "c"},
		{"shuffled, at a repeat", []int{2, 3, 0, 1}, 2, true, []string{"a", "c", "b"}, "a"},
		{"shuffled", []int{2, 3, 0, 1}, 3, true, []string{"a", "c", "b"}, "b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := Restore(queue, tc.order, tc.index, tc.shuffled)
			checkQueue(t, p, tc.want, tc.current)
			checkOnce(t, p)
			if p.IsShuffled() != tc.shuffled {
				t.Errorf("shuffled %v", p.IsShuffled())
			}
			if queue[2] != "a" {
				t.Error("Restore changed its argument")
			}
			// Once restored, it saves as it restores.
			again := Restore(p.Queued(), p.Order(), p.Index(), p.IsShuffled())
			checkQueue(t, again, tc.want, tc.current)
		})
	}
}

// TestQueueOnce runs random edits, with and without shuffle, and checks
// after each that every song is queued once and enqueueing keeps the
// current song.
func TestQueueOnce(t *testing.T) {
	pool := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	r := rand.New(rand.NewPCG(1, 2))
	pick := func() []string {
		ids := make([]string, r.IntN(6))
		for i := range ids {
			ids[i] = pool[r.IntN(len(pool))]
		}
		return ids
	}
	p := New(pick())
	for range 2000 {
		current := p.Current()
		switch op := r.IntN(8); op {
		case 0, 1:
			p.Enqueue(pick(), op == 0)
			if current != "" && p.Current() != current {
				t.Fatalf("enqueueing changed the current song from %q to %q", current, p.Current())
			}
		case 2:
			if ids := pick(); len(ids) > 0 {
				start := r.IntN(len(ids))
				p.PlayFrom(ids, start)
				if p.Current() != ids[start] {
					t.Fatalf("PlayFrom(%q, %d) plays %q", ids, start, p.Current())
				}
				if got := p.Queued(); !slices.Equal(got, firsts(ids)) {
					t.Fatalf("PlayFrom(%q, %d) queued %q", ids, start, got)
				}
			}
		case 3:
			p.ToggleShuffle()
		case 4:
			p.Play(pool[r.IntN(len(pool))])
		case 5:
			p.Remove(r.IntN(len(pool)))
		case 6:
			p.Move(r.IntN(len(pool)), r.IntN(len(pool)))
		case 7:
			p.Next()
		}
		checkOnce(t, p)
		if t.Failed() {
			t.Fatalf("after op on %q", playOrder(p))
		}
	}
}

// TestRestoreRepeatsRandom restores random queues with repeats: each song
// once, at its first place in the queue and in the play order, the current
// song kept.
func TestRestoreRepeatsRandom(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	for range 500 {
		queue := make([]string, 1+r.IntN(12))
		for i := range queue {
			queue[i] = string(rune('a' + r.IntN(6)))
		}
		order := r.Perm(len(queue))
		index := r.IntN(len(queue))
		var play []string
		for _, i := range order {
			play = append(play, queue[i])
		}
		p := Restore(queue, order, index, true)
		checkOnce(t, p)
		checkQueue(t, p, firsts(play), play[index])
		if got := p.Queued(); !slices.Equal(got, firsts(queue)) {
			t.Errorf("queued %q, want %q", got, firsts(queue))
		}
		if t.Failed() {
			t.Fatalf("restored %q, order %v, index %d", queue, order, index)
		}
	}
}

// firsts returns ids without repeats, each at its first place.
func firsts(ids []string) []string {
	var out []string
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}
