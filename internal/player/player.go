// Package player holds playback state: queue, current song and position,
// volume, shuffle and repeat. It plays no audio; package playback makes the
// engine follow it.
package player

import (
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
}

// New returns a paused player at the start of queue.
func New(queue []string) *Player {
	p := &Player{Volume: defaultVolume, queue: slices.Clone(queue)}
	p.resetOrder()
	return p
}

// resetOrder puts the queue back in sequence, keeping the current song.
func (p *Player) resetOrder() {
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
func (p *Player) PlayAll(ids []string) {
	if len(ids) == 0 {
		return
	}
	p.queue = slices.Clone(ids)
	p.order, p.at = nil, 0
	p.resetOrder()
	if p.isShuffled {
		p.shuffleRest()
	}
	p.Position, p.Playing = 0, true
}

// insert places id after the current song, in both queue and order, and
// returns its queue index.
func (p *Player) insert(id string) int {
	queueIndex := 0
	if len(p.order) > 0 {
		queueIndex = p.order[p.at] + 1
	}
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
