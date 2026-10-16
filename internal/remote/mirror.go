package remote

import (
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
)

// Mirror is the daemon's playback as it last reported it: the State, the
// queue and its songs. The Client changes it on the front-end's thread, in
// the funcs it posts; read it there only. A control changes it only once
// the daemon's event comes.
//
// After a reconnection, the first events (the daemon's snapshot) replace
// what the Mirror held.
type Mirror struct {
	state api.State
	// at is when state arrived, on this process's monotonic clock.
	at    time.Time
	queue []string
	songs map[string]catalog.Song
	// link is the connection the data came from (Client.links).
	link uint64

	stateGen, queueGen uint64
}

// State returns the playback state last reported.
func (m *Mirror) State() api.State { return m.state }

// Position returns the seconds into the current song at now (time.Now(),
// whose monotonic reading counts): the reported position moved on while
// playing.
func (m *Mirror) Position(now time.Time) float64 { return Position(m.state, m.at, now) }

// Position returns how far s, which arrived at at, has played by now: its
// Position plus the time since while Playing and not Waiting, at most its
// Duration. Both times must come from this process's clock: no time
// crosses the socket.
func Position(s api.State, at, now time.Time) float64 {
	pos := s.Position
	if s.Playing && !s.Waiting {
		pos += now.Sub(at).Seconds()
	}
	if s.Duration > 0 {
		pos = min(pos, s.Duration)
	}
	return max(pos, 0)
}

// Queue returns the queue's song IDs in play order. It is the Mirror's
// own: do not modify it.
func (m *Mirror) Queue() []string { return m.queue }

// Song returns the queue's song id.
func (m *Mirror) Song(id string) (catalog.Song, bool) {
	song, ok := m.songs[id]
	return song, ok
}

// Current returns the current song, if the Mirror knows it.
func (m *Mirror) Current() (catalog.Song, bool) {
	if m.state.Current == "" {
		return catalog.Song{}, false
	}
	return m.Song(m.state.Current)
}

// Songs returns the queue's songs the Mirror knows, in play order. It
// makes a new slice: memoize it on QueueGen.
func (m *Mirror) Songs() []catalog.Song {
	songs := make([]catalog.Song, 0, len(m.queue))
	for _, id := range m.queue {
		if song, ok := m.songs[id]; ok {
			songs = append(songs, song)
		}
	}
	return songs
}

// StateGen changes whenever the State does.
func (m *Mirror) StateGen() uint64 { return m.stateGen }

// QueueGen changes whenever the queue or its songs do.
func (m *Mirror) QueueGen() uint64 { return m.queueGen }

// apply keeps ev's playback, which arrived at at over connection link.
func (m *Mirror) apply(ev *api.Event, link uint64, at time.Time) {
	if link != m.link {
		// A new daemon, or the same one after a gap: its snapshot follows,
		// with every song of its queue.
		m.link = link
		clear(m.songs)
		m.queueGen++
	}
	if ev.State != nil {
		m.state, m.at = *ev.State, at
		m.stateGen++
	}
	if len(ev.Songs) > 0 {
		if m.songs == nil {
			m.songs = make(map[string]catalog.Song, len(ev.Songs))
		}
		for _, song := range ev.Songs {
			m.songs[song.ID] = song
		}
		m.queueGen++
	}
	if ev.Queue != nil {
		m.queue = ev.Queue.IDs
		m.prune()
		m.queueGen++
	}
}

// prune drops the songs no longer queued, once they are as many as those
// queued: now and then rather than on every change of the queue.
func (m *Mirror) prune() {
	if len(m.songs) <= 2*len(m.queue)+64 {
		return
	}
	kept := make(map[string]catalog.Song, len(m.queue))
	for _, id := range m.queue {
		if song, ok := m.songs[id]; ok {
			kept[id] = song
		}
	}
	m.songs = kept
}
