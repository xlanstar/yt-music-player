package remote

import (
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
)

// Mirror is the daemon's playback as last reported: the State, the queue
// and its songs. Read it on the front-end's thread, where the Client
// changes it; a reconnection's snapshot replaces it.
type Mirror struct {
	state api.State
	// at is when state arrived, on this process's monotonic clock.
	at    time.Time
	queue []string
	songs map[string]catalog.Song
	link  uint64 // the connection the data came from

	stateGen, queueGen uint64
	// isStale: songs came over an earlier link; the next queue drops them.
	isStale bool
}

// State returns the playback state last reported.
func (m *Mirror) State() api.State { return m.state }

// Position returns the seconds into the current song at now.
func (m *Mirror) Position(now time.Time) float64 { return Position(m.state, m.at, now) }

// Position returns how far s, which arrived at at, has played by now,
// both on this process's monotonic clock.
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

// Queue returns the queue's song IDs in play order; do not modify it.
func (m *Mirror) Queue() []string { return m.queue }

// Song returns the queue's song id.
func (m *Mirror) Song(id string) (catalog.Song, bool) {
	song, ok := m.songs[id]
	return song, ok
}

// StateGen changes whenever the State does.
func (m *Mirror) StateGen() uint64 { return m.stateGen }

// QueueGen changes whenever the queue or its songs do.
func (m *Mirror) QueueGen() uint64 { return m.queueGen }

func (m *Mirror) apply(ev *api.Event, link uint64, at time.Time) {
	if link != m.link {
		// A new daemon, or the same one after a gap: its snapshot follows,
		// every song of its queue ahead of the queue, which shows till then.
		m.link, m.isStale = link, true
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

// prune drops the songs no longer queued once they outnumber those
// queued, rather than on every change of the queue, or once stale.
func (m *Mirror) prune() {
	if !m.isStale && len(m.songs) <= 2*len(m.queue)+64 {
		return
	}
	m.isStale = false
	kept := make(map[string]catalog.Song, len(m.queue))
	for _, id := range m.queue {
		if song, ok := m.songs[id]; ok {
			kept[id] = song
		}
	}
	m.songs = kept
}
