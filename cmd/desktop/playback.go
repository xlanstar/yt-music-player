package main

import (
	"math"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"

	"github.com/egoist/mygo/ui"
)

// ytmd plays (docs/daemon.md): the views read its Mirror and drive
// it through the client, the Controller. The position moves on in the
// app, from the State's position and when it came, so ytmd sends nothing
// while a song plays; progress schedules the frames that show it.

const (
	// focusedStep is the shown position's step, in seconds, while the
	// window has keyboard focus.
	focusedStep = 1
	// blurredStep is the step while unfocused: the window is glanced at,
	// not watched. The Metal driver holds about 250 MB for two seconds
	// after each frame; 5 s steps give it back for 3 s of every 5.
	blurredStep = 5
	// tickSlack makes the position's frame land just after its step
	// turns.
	tickSlack = 15 * time.Millisecond
)

// progress decides when the shown position next needs a frame: as its
// step turns, every second while the window has the focus, on multiples of
// blurredStep while not (so the clock moves in whole steps), never while
// hidden, paused or waiting for the song. Song changes and seeks come as
// the daemon's States, each a build of its own.
type progress struct {
	// isBlurred: the window lost keyboard focus; isHidden: it is
	// minimized or hidden.
	isBlurred, isHidden bool
}

func (p *progress) setFocused(focused bool) { p.isBlurred = !focused }

func (p *progress) setShown(shown bool) { p.isHidden = !shown }

// isWatched reports whether the window is focused and shown: only then are
// animations that follow the song worth their frames.
func (p *progress) isWatched() bool { return !p.isBlurred && !p.isHidden }

// nextFrame returns how long after a frame showing pos of s the next one
// is due, or 0 when none is.
func (p *progress) nextFrame(s *api.State, pos float64) time.Duration {
	if !s.Playing || s.Waiting || s.Current == "" || p.isHidden || s.Duration > 0 && pos >= s.Duration {
		return 0
	}
	step := float64(focusedStep)
	if p.isBlurred {
		step = blurredStep
	}
	nextTurn := (math.Floor(pos/step) + 1) * step
	return time.Duration((nextTurn-pos)*float64(time.Second)) + tickSlack
}

// syncPlayback asks for the frame that moves the shown position on.
func (a *app) syncPlayback(c *ui.Context) {
	st := a.mirror.State()
	if next := a.progress.nextFrame(&st, a.position(c)); next > 0 {
		c.After(next)
	}
}

// position is the current song's, as of the frame being built.
func (a *app) position(c *ui.Context) float64 { return a.mirror.Position(c.Now()) }

// playingKey is what the current song and those up next derive from: the
// Mirror's State and queue, and the catalog, for songs the daemon did not
// describe.
type playingKey struct{ state, queue, music uint64 }

func (a *app) playingKey() playingKey {
	return playingKey{a.mirror.StateGen(), a.mirror.QueueGen(), a.cat.Gen()}
}

// currentID is the current song's ID, "" with an empty queue.
func (a *app) currentID() string { return a.currentSong().ID }

// isPlaying reports whether the daemon plays, or waits for a song to.
func (a *app) isPlaying() bool { return a.mirror.State().Playing }

// currentSong is the zero Song with an empty queue; a song the daemon did
// not describe has only its ID.
func (a *app) currentSong() catalog.Song {
	m := &a.memo.current
	if m.stale(a.playingKey()) {
		id := a.mirror.State().Current
		s, ok := a.mirror.Song(id)
		if !ok {
			s, ok = a.cat.Song(id)
		}
		if !ok {
			s = catalog.Song{ID: id}
		}
		m.v = s
	}
	return m.v
}

// upNextCount is how many songs the queue panel shows after the current.
const upNextCount = 8

// upNext returns the songs Next would go through, up to upNextCount, in
// play order and wrapping when repeating, without the current one.
func (a *app) upNext() []catalog.Song {
	m := &a.memo.upNext
	if !m.stale(a.playingKey()) {
		return m.v
	}
	st, queue := a.mirror.State(), a.mirror.Queue()
	songs := m.v[:0]
	for ahead := 1; ahead < len(queue) && ahead <= upNextCount; ahead++ {
		i := st.Index + ahead
		if i >= len(queue) {
			if st.Repeat != api.RepeatAll && st.Repeat != api.RepeatOne {
				break
			}
			i -= len(queue)
		}
		if s, ok := a.song(queue[i]); ok {
			songs = append(songs, s)
		}
	}
	m.v = songs
	return songs
}

// song is the queue's song id as the daemon described it, else as the
// catalog has it.
func (a *app) song(id string) (catalog.Song, bool) {
	if s, ok := a.mirror.Song(id); ok {
		return s, true
	}
	return a.cat.Song(id)
}

// playSong plays s as a row does: pauses or resumes it when current, jumps
// to it when queued, else plays it after the current song.
func (a *app) playSong(s *catalog.Song) {
	a.client.Play([]string{s.ID}, 0, []catalog.Song{*s})
}

// playAll makes ids the queue, from the first; the daemon learns the
// songs from the catalog.
func (a *app) playAll(ids []string) {
	if len(ids) == 0 {
		return
	}
	songs := make([]catalog.Song, 0, len(ids))
	for _, id := range ids {
		if s, ok := a.cat.Song(id); ok {
			songs = append(songs, s)
		}
	}
	a.client.PlayAll(ids, songs)
}

// held is a slider's value: the Mirror's, but while the user moves it
// (remote.Mirror). A State that comes meanwhile does not pull
// it from under the pointer, and once let go it waits for the State that
// answers the last change before following the Mirror again.
type held struct {
	// v is bound to the slider; shown is what the last build put there,
	// so a difference is the user's.
	v, shown  float64
	isPressed bool
	// isSent: the user's value went to the daemon while the Mirror's
	// StateGen was sentGen.
	isSent  bool
	sentGen uint64
}

// sync readies h for a build: it returns whether the user moved the
// slider since the last, to the value to send (h.v); otherwise h shows
// mirrored, the Mirror's value of StateGen gen, unless held.
func (h *held) sync(mirrored float64, gen uint64) (moved bool) {
	switch {
	case h.v != h.shown:
		moved = true
		h.isSent, h.sentGen = true, gen
	case h.isPressed, h.isSent && gen == h.sentGen:
	default:
		h.isSent = false
		h.v = mirrored
	}
	h.shown = h.v
	return moved
}

// release records whether the slider is pressed, after its build.
func (h *held) release(slider ui.Element) { h.isPressed = slider.Pressed() }
