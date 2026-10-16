// Package player holds playback state: queue, current song and position,
// volume, shuffle and repeat. It plays no audio; package playback makes the
// engine follow it.
package player

import (
	"math"
	"math/rand/v2"
	"slices"
)

const (
	// restartAfter is the position, in seconds, past which Prev restarts
	// the song instead of going back, as most players do.
	restartAfter = 3
	// unmuteVolume is restored when unmuting at zero volume, so unmuting
	// is audible.
	unmuteVolume  = 0.5
	defaultVolume = 0.7
)

// Repeat is what plays after a song or the queue ends.
type Repeat uint8

// Repeat modes, in CycleRepeat order.
const (
	RepeatOff Repeat = iota
	RepeatAll
	RepeatOne
	repeatModes
)

// Player is the playback state. Exported fields are plain values so UI
// controls can bind to them; the queue changes only through methods.
type Player struct {
	Playing  bool
	Position float64 // seconds into the current song
	Volume   float64 // 0 to 1
	Muted    bool
	Repeat   Repeat

	queue []string
	// order is the play order as indices into queue, in sequence or
	// shuffled; at is the current song's index in order.
	order      []int
	at         int
	isShuffled bool
	// gen counts changes to queue and order.
	gen uint64
}

// New returns a paused player at the start of queue.
func New(queue []string) *Player {
	p := &Player{Volume: defaultVolume, queue: slices.Clone(queue)}
	p.resetOrder()
	return p
}

// Restore returns a paused player over queue as Queued and Order gave it,
// at index in play order: what a daemon saved. An order that is not one of
// queue's is put back in sequence, unshuffled.
func Restore(queue []string, order []int, index int, shuffled bool) *Player {
	p := &Player{Volume: defaultVolume, queue: slices.Clone(queue)}
	if isPermutation(order, len(queue)) {
		p.order, p.isShuffled = slices.Clone(order), shuffled
	} else {
		p.resetOrder()
	}
	p.at = min(max(index, 0), max(len(p.order)-1, 0))
	return p
}

func isPermutation(order []int, n int) bool {
	if len(order) != n {
		return false
	}
	seen := make([]bool, n)
	for _, i := range order {
		if i < 0 || i >= n || seen[i] {
			return false
		}
		seen[i] = true
	}
	return true
}

// resetOrder puts the queue back in sequence, keeping the current song.
func (p *Player) resetOrder() {
	p.gen++
	current := 0
	if p.at < len(p.order) {
		current = p.order[p.at]
	}
	p.order = make([]int, len(p.queue))
	for i := range p.order {
		p.order[i] = i
	}
	p.at = min(current, max(len(p.order)-1, 0))
}

// Current returns the ID of the current song, "" with an empty queue.
func (p *Player) Current() string {
	if len(p.order) == 0 {
		return ""
	}
	return p.queue[p.order[p.at]]
}

// Len returns the number of songs in the queue.
func (p *Player) Len() int { return len(p.queue) }

// Index returns the current song's index in play order, 0 with an empty
// queue.
func (p *Player) Index() int { return p.at }

// QueueGen changes whenever the queue or its play order does, so a daemon
// tells its front-ends only then.
func (p *Player) QueueGen() uint64 { return p.gen }

// AppendPlayOrder appends the queue's songs to dst in play order: shuffled
// while shuffling.
func (p *Player) AppendPlayOrder(dst []string) []string {
	for _, i := range p.order {
		dst = append(dst, p.queue[i])
	}
	return dst
}

// Queued returns a copy of the queue in the order its songs came, which
// Order indexes.
func (p *Player) Queued() []string { return slices.Clone(p.queue) }

// Order returns a copy of the play order, as indices into Queued.
func (p *Player) Order() []int { return slices.Clone(p.order) }

// Play starts song id from the beginning, or pauses or resumes it when it
// is already current. A song outside the queue is inserted after the
// current one.
func (p *Player) Play(id string) {
	if id == p.Current() && id != "" {
		p.Playing = !p.Playing
		return
	}
	queueIndex := slices.Index(p.queue, id)
	if queueIndex < 0 {
		queueIndex = p.insert(id)
	}
	p.at = slices.Index(p.order, queueIndex)
	p.Position, p.Playing = 0, true
}

// PlayAll makes ids the queue and plays it from the first song; while
// shuffling, the rest follow in random order.
func (p *Player) PlayAll(ids []string) { p.PlayFrom(ids, 0) }

// PlayFrom makes ids the queue and plays it from ids[start]; while
// shuffling, that song comes first and the others follow in random order.
func (p *Player) PlayFrom(ids []string, start int) {
	if len(ids) == 0 {
		return
	}
	p.queue = slices.Clone(ids)
	p.order, p.at = nil, 0
	p.resetOrder()
	p.at = min(max(start, 0), len(p.order)-1)
	if p.isShuffled {
		p.shuffleRest()
	}
	p.Position, p.Playing = 0, true
}

// Enqueue adds ids after the current song, with next, or else at the end
// of the play order. Into an empty queue, the first becomes current,
// paused.
func (p *Player) Enqueue(ids []string, next bool) {
	k := len(ids)
	if k == 0 {
		return
	}
	// Unshuffled, order stays the identity, as queue and order grow at the
	// same index.
	queueIndex, orderIndex := len(p.queue), len(p.order)
	if next && len(p.order) > 0 {
		queueIndex, orderIndex = p.order[p.at]+1, p.at+1
	}
	p.queue = slices.Insert(p.queue, queueIndex, ids...)
	for j, i := range p.order {
		if i >= queueIndex {
			p.order[j] = i + k
		}
	}
	n := len(p.order)
	p.order = slices.Grow(p.order, k)[:n+k]
	copy(p.order[orderIndex+k:], p.order[orderIndex:n])
	for j := range k {
		p.order[orderIndex+j] = queueIndex + j
	}
	p.gen++
}

// Remove takes the song at index in play order out of the queue. The song
// after a current one removed becomes current, from its start; past the
// end it wraps, and stops unless repeating.
func (p *Player) Remove(index int) {
	if index < 0 || index >= len(p.order) {
		return
	}
	queueIndex := p.order[index]
	p.queue = slices.Delete(p.queue, queueIndex, queueIndex+1)
	p.order = slices.Delete(p.order, index, index+1)
	for j, i := range p.order {
		if i > queueIndex {
			p.order[j] = i - 1
		}
	}
	p.gen++
	switch {
	case len(p.order) == 0:
		p.at, p.Position, p.Playing = 0, 0, false
	case index < p.at:
		p.at--
	case index == p.at:
		p.Position = 0
		if p.at == len(p.order) {
			p.at = 0
			if p.Repeat == RepeatOff {
				p.Playing = false
			}
		}
	}
}

// Move moves the song at from in play order to to; the current song stays
// current wherever it goes. Unshuffled, the queue itself changes order, so
// unshuffling after a shuffle keeps the move.
func (p *Player) Move(from, to int) {
	n := len(p.order)
	if from < 0 || from >= n || to < 0 || to >= n || from == to {
		return
	}
	if p.isShuffled {
		move(p.order, from, to)
	} else {
		move(p.queue, from, to)
	}
	switch {
	case p.at == from:
		p.at = to
	case from < p.at && to >= p.at:
		p.at--
	case from > p.at && to <= p.at:
		p.at++
	}
	p.gen++
}

// move moves s[from] to s[to], shifting what lies between.
func move[T any](s []T, from, to int) {
	v := s[from]
	if from < to {
		copy(s[from:to], s[from+1:to+1])
	} else {
		copy(s[to+1:from+1], s[to:from])
	}
	s[to] = v
}

// ClearUpNext removes the songs after the current one in play order.
func (p *Player) ClearUpNext() {
	if p.at+1 >= len(p.order) {
		return
	}
	kept := make([]string, p.at+1)
	for i := range kept {
		kept[i] = p.queue[p.order[i]]
	}
	p.queue, p.order = kept, nil
	at := p.at
	p.resetOrder()
	p.at = at
}

// Clear empties the queue and stops.
func (p *Player) Clear() {
	p.queue, p.order, p.at = p.queue[:0], p.order[:0], 0
	p.Position, p.Playing = 0, false
	p.gen++
}

// insert places id after the current song, in both queue and order, and
// returns its queue index.
func (p *Player) insert(id string) int {
	queueIndex := 0
	if len(p.order) > 0 {
		queueIndex = p.order[p.at] + 1
	}
	p.gen++
	p.queue = slices.Insert(p.queue, queueIndex, id)
	for k, i := range p.order {
		if i >= queueIndex {
			p.order[k] = i + 1
		}
	}
	orderIndex := 0
	if len(p.order) > 0 {
		orderIndex = p.at + 1
	}
	p.order = slices.Insert(p.order, orderIndex, queueIndex)
	return queueIndex
}

// TogglePlay pauses or resumes.
func (p *Player) TogglePlay() { p.Playing = !p.Playing }

// Pause pauses.
func (p *Player) Pause() { p.Playing = false }

// Resume plays the current song, if any.
func (p *Player) Resume() { p.Playing = p.Current() != "" }

// Seek moves to sec seconds into the current song, if any.
func (p *Player) Seek(sec float64) {
	if p.Current() != "" && !math.IsNaN(sec) {
		p.Position = max(sec, 0)
	}
}

// SetVolume sets the volume, kept within 0 to 1, and unmutes: moving the
// volume is meant to be heard.
func (p *Player) SetVolume(v float64) {
	if !math.IsNaN(v) {
		p.Volume, p.Muted = min(max(v, 0), 1), false
	}
}

// Next skips to the next song. Past the end it wraps, and stops unless
// repeating.
func (p *Player) Next() {
	if len(p.order) == 0 {
		return
	}
	p.Position = 0
	if p.at+1 < len(p.order) {
		p.at++
		return
	}
	p.at = 0
	if p.Repeat == RepeatOff {
		p.Playing = false
	}
}

// Prev restarts a song under way, else goes back one song; from the first
// song it wraps only when repeating.
func (p *Player) Prev() {
	restart := p.Position > restartAfter
	p.Position = 0
	if restart || len(p.order) == 0 {
		return
	}
	switch {
	case p.at > 0:
		p.at--
	case p.Repeat != RepeatOff:
		p.at = len(p.order) - 1
	}
}

// Finished moves on when the current song ends by itself. Unlike Next, it
// replays the song under RepeatOne.
func (p *Player) Finished() {
	if p.Repeat == RepeatOne {
		p.Position = 0
		return
	}
	p.Next()
}

// AppendUpNext appends to dst up to n songs Next would go through, in
// shuffle order and wrapping when repeating, excluding the current song.
// It takes dst because views call it every frame.
func (p *Player) AppendUpNext(dst []string, n int) []string {
	for ahead := 1; ahead < len(p.order) && ahead <= n; ahead++ {
		i := p.at + ahead
		if i >= len(p.order) {
			if p.Repeat == RepeatOff {
				break
			}
			i -= len(p.order)
		}
		dst = append(dst, p.queue[p.order[i]])
	}
	return dst
}

// IsShuffled reports whether the queue plays in random order.
func (p *Player) IsShuffled() bool { return p.isShuffled }

// ToggleShuffle shuffles the songs after the current one, or puts the
// queue back in order; the current song stays either way.
func (p *Player) ToggleShuffle() {
	p.isShuffled = !p.isShuffled
	if p.isShuffled {
		p.shuffleRest()
		return
	}
	p.resetOrder()
}

// shuffleRest puts the current song first in order, the others after it
// at random.
func (p *Player) shuffleRest() {
	if len(p.order) == 0 {
		return
	}
	current := p.order[p.at]
	// One allocation: current, then the rest.
	order := make([]int, 1, len(p.order))
	order[0] = current
	for _, i := range p.order {
		if i != current {
			order = append(order, i)
		}
	}
	p.gen++
	rest := order[1:]
	//nolint:gosec // a play order, no secret
	rand.Shuffle(len(rest), func(i, j int) { rest[i], rest[j] = rest[j], rest[i] })
	p.order, p.at = order, 0
}

// CycleRepeat goes from off to all to one, then back to off.
func (p *Player) CycleRepeat() { p.Repeat = (p.Repeat + 1) % repeatModes }

// IsSilent reports whether nothing would be heard: muted or at zero volume.
func (p *Player) IsSilent() bool { return p.Muted || p.Volume == 0 }

// ToggleMute mutes or unmutes; unmuting at zero volume also raises it.
func (p *Player) ToggleMute() {
	if !p.IsSilent() {
		p.Muted = true
		return
	}
	p.Muted = false
	if p.Volume == 0 {
		p.Volume = unmuteVolume
	}
}
