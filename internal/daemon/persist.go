package daemon

import (
	"cmp"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/player"

	"github.com/rs/zerolog/log"
)

// stateVersion is daemon.json's "v"; a file of another is set aside.
const stateVersion = 1

// savedState is daemon.json. The queue's songs are kept with it, compact
// (zero fields left out), as the daemon has no catalog to describe them
// on the next start.
type savedState struct {
	V        int           `json:"v"`
	Queue    []string      `json:"queue"`
	Index    int           `json:"index"`
	Order    []int         `json:"order"`
	Position float64       `json:"position"`
	Volume   float64       `json:"volume"`
	Muted    bool          `json:"muted"`
	Repeat   player.Repeat `json:"repeat"`
	Shuffled bool          `json:"shuffled"`
	Songs    []savedSong   `json:"songs,omitzero"`
	// ResumePlaying, written by a hand-off, has the next daemon play on if
	// it starts before ResumeBefore; read once.
	ResumePlaying bool      `json:"resumePlaying,omitzero"`
	ResumeBefore  time.Time `json:"resumeBefore,omitzero"`
}

// savedSong is a catalog.Song in daemon.json.
type savedSong struct {
	ID      string      `json:"id"`
	Title   string      `json:"title,omitzero"`
	Artist  string      `json:"artist,omitzero"`
	Album   string      `json:"album,omitzero"`
	AlbumID string      `json:"albumId,omitzero"`
	Seconds int         `json:"seconds,omitzero"`
	Art     catalog.Art `json:"art,omitzero"`
}

// saveMark is what, changed, saves the state after the delay; the
// position saves at once on a pause, a seek or a new song instead.
type saveMark struct {
	queueGen uint64
	index    int
	volume   float64
	muted    bool
	repeat   player.Repeat
	shuffled bool
}

func (s *Service) mark() saveMark {
	p := s.player
	return saveMark{p.QueueGen(), p.Index(), p.Volume, p.Muted, p.Repeat, p.IsShuffled()}
}

// loadState reads daemon.json; ok is false without one. One unreadable is
// kept aside as daemon.json.bad, and the daemon starts empty.
func loadState(path string) (st savedState, ok bool) {
	if path == "" {
		return savedState{}, false
	}
	data, err := os.ReadFile(path) //nolint:gosec // Paths.State, not input
	if errors.Is(err, fs.ErrNotExist) {
		return savedState{}, false
	}
	if err == nil {
		err = json.Unmarshal(data, &st)
	}
	if err == nil && st.V != stateVersion {
		err = fmt.Errorf("version %d", st.V)
	}
	if err != nil {
		log.Warn().Err(err).Msg("unreadable state, kept aside")
		if err := os.Rename(path, path+".bad"); err != nil {
			log.Debug().Err(err).Msg("keep the state aside")
		}
		return savedState{}, true
	}
	return st, true
}

// restore makes the player and the catalog from saved, or from seed when
// nothing was saved: paused, unless a hand-off asked to play on in time.
func (s *Service) restore(saved savedState, ok bool, seed []catalog.Song) {
	if !ok {
		ids := make([]string, len(seed))
		for i, song := range seed {
			ids[i] = song.ID
		}
		s.cat = catalog.New(seed, nil, nil)
		s.player = player.New(ids)
		return
	}
	songs := make([]catalog.Song, len(saved.Songs))
	for i, song := range saved.Songs {
		songs[i] = catalog.Song(song)
	}
	s.cat = catalog.New(songs, nil, nil)
	p := player.Restore(saved.Queue, saved.Order, saved.Index, saved.Shuffled)
	if p.Current() != "" {
		p.Position = max(saved.Position, 0)
	}
	if saved.V == stateVersion {
		p.Volume, p.Muted = min(max(saved.Volume, 0), 1), saved.Muted
	}
	if api.Repeat(saved.Repeat + 1).Valid() {
		p.Repeat = saved.Repeat
	}
	s.player = p
	s.resume = saved.ResumePlaying && s.now().Before(saved.ResumeBefore) && p.Current() != ""
}

// saved is the state to save now. Its slices are its own: it is written
// off the loop.
func (s *Service) saved() *savedState {
	p := s.player
	songs := make([]savedSong, len(s.cat.Songs))
	for i, song := range s.cat.Songs {
		songs[i] = savedSong(song)
	}
	return &savedState{
		V:        stateVersion,
		Queue:    p.Queued(),
		Index:    p.Index(),
		Order:    p.Order(),
		Position: p.Position,
		Volume:   p.Volume,
		Muted:    p.Muted,
		Repeat:   p.Repeat,
		Shuffled: p.IsShuffled(),
		Songs:    songs,
	}
}

// persist saves the position at once on a pause, a seek or a new song
// since last, the State told before, and the rest a while after it
// changes. Nothing waits while idle.
func (s *Service) persist(st, last api.State, jumped bool) {
	if s.store == nil {
		return
	}
	t := &s.sent
	paused := last.Playing && !st.Playing
	newSong := st.Current != last.Current
	mark := s.mark()
	changed := mark != t.seen
	t.seen = mark
	if paused || newSong || jumped {
		s.save()
		return
	}
	if changed && mark != t.mark {
		s.store.arm(s.post, s.saveDue)
	}
}

// saveDue runs when the delay after a change ends.
func (s *Service) saveDue() {
	if !s.stopping && s.mark() != s.sent.mark {
		s.save()
	}
}

// save writes the state off the loop.
func (s *Service) save() {
	if s.store == nil {
		return
	}
	s.sent.mark = s.mark()
	s.store.save(s.saved())
}

// store writes daemon.json: whole or not at all, the latest of what it is
// given, on a goroutine of its own while there is something to write.
type store struct {
	path  string
	delay time.Duration
	timer *time.Timer // the delay after a change; nil until one

	mu        sync.Mutex
	pending   *savedState
	isRunning bool
	idle      *sync.Cond // signaled when the writer returns
}

func newStore(path string, delay time.Duration) *store {
	w := &store{path: path, delay: delay}
	w.idle = sync.NewCond(&w.mu)
	return w
}

// arm has due run on the loop after the delay, or later if armed again
// meanwhile.
func (w *store) arm(post func(func()), due func()) {
	if w.timer == nil {
		w.timer = time.AfterFunc(w.delay, func() { post(due) })
		return
	}
	w.timer.Reset(w.delay)
}

// save writes st off the caller's goroutine.
func (w *store) save(st *savedState) {
	if w.timer != nil {
		w.timer.Stop()
	}
	w.mu.Lock()
	w.pending = st
	if w.isRunning {
		w.mu.Unlock()
		return
	}
	w.isRunning = true
	w.mu.Unlock()
	go w.drain()
}

func (w *store) drain() {
	for {
		w.mu.Lock()
		st := w.pending
		w.pending = nil
		if st == nil {
			w.isRunning = false
			w.idle.Broadcast()
			w.mu.Unlock()
			return
		}
		w.mu.Unlock()
		w.write(st)
	}
}

// wait returns once no write is under way.
func (w *store) wait() {
	w.mu.Lock()
	for w.isRunning {
		w.idle.Wait()
	}
	w.mu.Unlock()
}

func (w *store) write(st *savedState) {
	data, err := json.Marshal(st)
	if err == nil {
		err = replaceFile(w.path, data)
	}
	if err != nil {
		log.Warn().Err(err).Msg("save state")
	}
}

// replaceFile writes a temporary file renamed over path, which a crash
// midway leaves as it was. CreateTemp makes it 0600.
func replaceFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	err = cmp.Or(err, tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}
