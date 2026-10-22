// Package player holds playback state: queue, current song and position,
// volume, shuffle and repeat. It plays no audio; package playback makes the
// engine follow it.
//
// The queue holds each song ID once, as everything that plays, jumps to or
// shows a queued song names it by ID: a list played keeps each song's first
// place, adding a queued song adds nothing, and playing it next moves it.
package player

import (
	"math"
	"math/rand/v2"
	"slices"
	"strings"
)

const (
	// restartAfter is the position, in seconds, past which Prev restarts
	// the song instead of going back, as most players do.
	restartAfter = 3
	// unmuteVolume is restored when unmuting at zero volume, so unmuting
	// is audible.
	unmuteVolume  = 0.5
	defaultVolume = 0.7
	// fewIDs is how many IDs Enqueue looks for in the queue one by one;
	// past it, it indexes the queue, which reaches thousands of songs.
	fewIDs = 16
)

// Repeat is what plays after a song or the queue ends.
type Repeat uint8

// Repeat modes, in CycleRepeat order. Their values are daemon.json's
// "repeat" too: add new ones only at the end, never reorder.
const (
	RepeatOff Repeat = iota
	RepeatAll
	RepeatOne
	repeatModes
)

// Valid reports whether r is a repeat mode, as one read from a file may not.
func (r Repeat) Valid() bool { return r < repeatModes }

// Player is the playback state. Exported fields are plain values so UI
// controls can bind to them; the queue changes only through methods.
type Player struct {
	Position float64 // seconds into the current song
	Volume   float64 // 0 to 1

	queue []string
	// order is the play order as indices into queue, in sequence or
	// shuffled; at is the current song's index in order.
	order []int
	at    int
	// gen counts changes to queue and order.
	gen uint64

	Playing    bool
	Muted      bool
	Repeat     Repeat
	isShuffled bool
}

// New returns a paused player at the start of queue.
func New(queue []string) *Player {
	p := &Player{Volume: defaultVolume}
	p.setQueue(queue, 0)
	return p
}

// Saved is the state a daemon keeps of a player between runs.
type Saved struct {
	// Queue and Order are as Queued and Order gave them; Index is the
	// current song's in play order.
	Queue    []string
	Order    []int
	Index    int
	Position float64
	Volume   float64
	Muted    bool
	Repeat   Repeat
	Shuffled bool
}

// Restore returns a paused player as s saved it, over copies of its slices.
// What a file may hold is put right: an order not the queue's is put back
// in sequence, unshuffled; a song queued twice keeps its first place; the
// position and volume are kept in range; an unknown repeat mode is off.
func Restore(s Saved) *Player {
	p := &Player{queue: slices.Clone(s.Queue)}
	if isPermutation(s.Order, len(s.Queue)) {
		p.order, p.isShuffled = slices.Clone(s.Order), s.Shuffled
	} else {
		p.resetOrder()
	}
	p.at = min(max(s.Index, 0), max(len(p.order)-1, 0))
	p.dropRepeats()
	p.Seek(s.Position)
	p.SetVolume(s.Volume)
	p.Muted = s.Muted
	if s.Repeat.Valid() {
		p.Repeat = s.Repeat
	}
	return p
}

// dropRepeats keeps each song's first place in the queue, and in the play
// order its first place there; the current song stays current. Only a
// state saved before the queue held each song once has repeats.
func (p *Player) dropRepeats() {
	n := len(p.queue)
	if n < 2 {
		return
	}
	byID := make([]int, n)
	sortByID(p.queue, byID)
	// to is each queue index's first place, made once a repeat shows.
	var to []int
	first := byID[0]
	for _, i := range byID[1:] {
		if p.queue[i] != p.queue[first] {
			first = i
			continue
		}
		if to == nil {
			to = make([]int, n)
			for j := range to {
				to[j] = j
			}
		}
		to[i] = first
	}
	if to == nil {
		return
	}
	// Firsts come before their repeats, so a repeat's first has its new
	// index already.
	kept := 0
	for i, f := range to {
		if f != i {
			to[i] = to[f]
			continue
		}
		to[i] = kept
		p.queue[kept] = p.queue[i]
		kept++
	}
	clear(p.queue[kept:])
	p.queue = p.queue[:kept]
	current := to[p.order[p.at]]
	placed := make([]bool, kept)
	order := p.order[:0]
	for _, i := range p.order {
		if j := to[i]; !placed[j] {
			placed[j] = true
			order = append(order, j)
		}
	}
	p.order = order
	p.at = slices.Index(order, current)
	p.gen++
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
	// The player alone holds its order (Order copies): reuse its array.
	p.order = sized(p.order, len(p.queue))
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
	n := len(dst)
	dst = slices.Grow(dst, len(p.order))[:n+len(p.order)]
	for j, i := range p.order {
		dst[n+j] = p.queue[i]
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

// PlayAll makes ids the queue and plays it from ids[start]; while
// shuffling, that song comes first and the others follow in random order.
// A song in ids more than once is queued at its first place.
func (p *Player) PlayAll(ids []string, start int) {
	if len(ids) == 0 {
		return
	}
	p.setQueue(ids, start)
	if p.isShuffled {
		p.shuffleRest()
	}
	p.Position, p.Playing = 0, true
}

// setQueue makes ids, each once, the queue in sequence, at ids[start].
func (p *Player) setQueue(ids []string, start int) {
	p.gen++
	// The player alone holds its queue and order (Queued and Order copy):
	// reuse their arrays, the old songs past the new ones cleared.
	old := len(p.queue)
	p.queue = append(p.queue[:0], ids...)
	if old > len(ids) {
		clear(p.queue[len(ids):old])
	}
	p.order = sized(p.order, len(ids))
	p.queue, p.at = dedupe(p.queue, min(max(start, 0), max(len(ids)-1, 0)), p.order)
	p.order = p.order[:len(p.queue)]
	for i := range p.order {
		p.order[i] = i
	}
}

// sized returns order n long, over its array when that has room; else a
// new one of exactly n, so that Enqueue's growth starts from n.
func sized(order []int, n int) []int {
	if cap(order) < n {
		return make([]int, n)
	}
	return order[:n]
}

// dedupe removes from q, in place, each ID after its first, and returns q
// so shortened and where q[start] went. scratch, as long as q, is
// overwritten.
func dedupe(q []string, start int, scratch []int) ([]string, int) {
	if len(q) < 2 {
		return q, start
	}
	sortByID(q, scratch)
	// The repeats' indexes overwrite scratch's sorted ones, never ahead of
	// those still to read.
	repeats := scratch[:0]
	prev := scratch[0]
	for _, i := range scratch[1:] {
		if q[i] == q[prev] {
			repeats = append(repeats, i)
		}
		prev = i
	}
	if len(repeats) == 0 {
		return q, start
	}
	slices.Sort(repeats)
	id, at := q[start], -1
	kept := q[:0]
	for i, s := range q {
		if len(repeats) > 0 && repeats[0] == i {
			repeats = repeats[1:]
			continue
		}
		if at < 0 && s == id {
			at = len(kept)
		}
		kept = append(kept, s)
	}
	clear(q[len(kept):])
	return kept, at
}

// sortByID fills idx, as long as q, with q's indexes sorted by ID; equal
// IDs by index, so each ID's run starts at its first place. Sorting rather
// than hashing allocates nothing for queues of thousands of songs.
func sortByID(q []string, idx []int) {
	for i := range idx {
		idx[i] = i
	}
	slices.SortFunc(idx, func(a, b int) int {
		if c := strings.Compare(q[a], q[b]); c != 0 {
			return c
		}
		return a - b
	})
}

// Enqueue adds ids after the current song, with next, or else at the end
// of the play order. Into an empty queue, the first becomes current,
// paused. Each song is queued once: a song already queued stays where it
// is, or with next moves after the current one, which itself stays.
func (p *Player) Enqueue(ids []string, next bool) {
	ids, queued := p.toEnqueue(ids, next)
	k := len(ids)
	if k == 0 {
		return
	}
	if len(queued) > 0 {
		p.removeQueued(queued)
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

// handled marks, in toEnqueue's index of the queue, an ID already seen in
// ids.
const handled = -2

// toEnqueue returns the ids Enqueue adds, each once, without the current
// song or, unless next, songs already queued; ids itself when it drops
// none. With next, it also returns the queue indexes of the songs that
// move, already queued.
func (p *Player) toEnqueue(ids []string, next bool) (add []string, queued []int) {
	var index map[string]int
	if len(ids) > fewIDs {
		index = make(map[string]int, len(p.queue)+len(ids))
		for i, id := range p.queue {
			index[id] = i
		}
	}
	current := -1
	if len(p.order) > 0 {
		current = p.order[p.at]
	}
	dropped := false
	for j, id := range ids {
		i := -1
		switch {
		case index != nil:
			if at, ok := index[id]; ok {
				i = at
			}
			index[id] = handled
		case slices.Contains(ids[:j], id):
			i = handled
		default:
			i = slices.Index(p.queue, id)
		}
		if i == handled || (i >= 0 && (i == current || !next)) {
			if !dropped {
				dropped, add = true, slices.Clone(ids[:j])
			}
			continue
		}
		if i >= 0 {
			queued = append(queued, i)
		}
		if dropped {
			add = append(add, id)
		}
	}
	if !dropped {
		add = ids
	}
	return add, queued
}

// removeQueued takes the songs at queue indexes out of the queue and its
// play order, none of them the current song, which stays current.
func (p *Player) removeQueued(indexes []int) {
	slices.Sort(indexes)
	removed := func(i int) bool {
		_, ok := slices.BinarySearch(indexes, i)
		return ok
	}
	kept := p.queue[:0]
	for i, id := range p.queue {
		if !removed(i) {
			kept = append(kept, id)
		}
	}
	clear(p.queue[len(kept):])
	p.queue = kept
	order, at := p.order[:0], p.at
	for j, i := range p.order {
		if removed(i) {
			if j < p.at {
				at--
			}
			continue
		}
		// Less the removed indexes before it.
		before, _ := slices.BinarySearch(indexes, i)
		order = append(order, i-before)
	}
	p.order, p.at = order, at
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
	p.queue = kept
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

// TogglePlay pauses, or resumes the current song, if any.
func (p *Player) TogglePlay() { p.Playing = !p.Playing && p.Current() != "" }

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

// Next skips to the next song, wrapping past the end. Asked for, it plays
// on even without repeat, as Prev does: only a queue running out by
// itself stops (Finished, Skip).
func (p *Player) Next() { p.advance() }

// Skip moves past a song that cannot play: as Next, but past the end it
// stops unless repeating.
func (p *Player) Skip() {
	if p.advance() && p.Repeat == RepeatOff {
		p.Playing = false
	}
}

// advance moves to the next song and reports whether it wrapped past the
// end.
func (p *Player) advance() (wrapped bool) {
	if len(p.order) == 0 {
		return false
	}
	p.Position = 0
	if p.at+1 < len(p.order) {
		p.at++
		return false
	}
	p.at = 0
	return true
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
// replays the song under RepeatOne and stops past the end unless
// repeating.
func (p *Player) Finished() {
	if p.Repeat == RepeatOne {
		p.Position = 0
		return
	}
	p.Skip()
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
	// In place: current first, then the rest, shuffled.
	order := p.order
	order[0], order[p.at] = order[p.at], order[0]
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
