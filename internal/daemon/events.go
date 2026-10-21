package daemon

import (
	"math"
	"slices"
	"time"
	"yt-music-player/internal/account"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/ipc"
	"yt-music-player/internal/notice"
	"yt-music-player/internal/version"
)

// jumpSlack is how far, in seconds, the position may stray from where the
// last State put it before it counts as a seek: the engine's clock and the
// loop's drift apart a little.
const jumpSlack = 1

const (
	// songsPerFrame bounds the songs of an event (about 70 KB): gob keeps a
	// connection's buffer as large as the largest frame it sent.
	songsPerFrame = 256
	// maxSongFrames bounds a list's frames, so that a snapshot's two lists
	// fill at most half a connection's queue, whose overflow closes it.
	maxSongFrames = ipc.QueueLen / 4
)

// sentState is what the subscribers were last told and the state file
// last kept, to send and save only what changed.
type sentState struct {
	state api.State
	at    time.Time // when state was sent
	// queueGen is the player's QueueGen when the queue was last told;
	// queued holds its IDs, spare the next queued's room.
	queueGen      uint64
	queued, spare map[string]struct{}
	// catGen is the catalog's Gen once it held only the queue's songs.
	catGen uint64
	// seen is the latest seen, to save after a change; mark is what the
	// state file holds.
	seen, mark saveMark
}

// reset takes the Service's state as told and saved.
func (t *sentState) reset(s *Service) {
	t.state, t.at = s.state(), s.now()
	t.queueGen = s.player.QueueGen()
	t.queued, t.spare = map[string]struct{}{}, map[string]struct{}{}
	for _, id := range s.player.Queued() {
		t.queued[id] = struct{}{}
	}
	t.catGen = s.cat.Gen()
	t.seen = s.mark()
	t.mark = t.seen
}

func (s *Service) state() api.State {
	p := s.player
	current := p.Current()
	song, ok := s.cat.Song(current)
	if !ok {
		song.ID = current
	}
	return api.State{
		Playing:  p.Playing,
		Waiting:  s.pb.IsWaiting(),
		Position: p.Position,
		Duration: s.pb.SongLength(song),
		Volume:   p.Volume,
		Muted:    p.Muted,
		Repeat:   api.Repeat(p.Repeat) + 1,
		Shuffled: p.IsShuffled(),
		Current:  current,
		Index:    p.Index(),
	}
}

// settle runs after each batch on the loop: the engine follows, then the
// subscribers hear what changed and the state saves. A batch that changed
// nothing allocates nothing.
func (s *Service) settle() {
	if s.stopping {
		return
	}
	s.pb.Follow()
	last := s.sent.state
	queueChanged := s.player.QueueGen() != s.sent.queueGen
	st, now := s.state(), s.now()
	changed, jumped := s.compare(st, now)
	n, hasNotice := s.pb.TakeNotice()
	if changed || jumped || queueChanged || hasNotice {
		s.tell(st, now, queueChanged, n, hasNotice)
	}
	if s.cat.Gen() != s.sent.catGen {
		// Songs sent with a song played that was queued already.
		s.keepQueued()
	}
	s.persist(st, last, jumped)
	s.quiet.Note()
}

// tell sends the State, with the queue if it changed and the notice.
func (s *Service) tell(st api.State, now time.Time, queueChanged bool, n notice.Notice, hasNotice bool) {
	ev := &api.Event{State: &st}
	s.sent.state, s.sent.at = st, now
	var ahead []api.Frame
	if queueChanged {
		s.sent.queueGen = s.player.QueueGen()
		ids := s.player.AppendPlayOrder(make([]string, 0, s.player.Len()))
		ev.Queue = &api.Queue{IDs: ids}
		ahead, ev.Songs = carry(nil, s.newSongs(ids), queuedSongs)
	}
	if hasNotice {
		// A copy, so that n stays on the stack when there is none.
		told := n
		ev.Notice = &told
	}
	s.broadcast(ahead, ev)
}

// compare reports whether st differs from the State last sent but in its
// position, and whether the position jumped from where that State put it.
func (s *Service) compare(st api.State, now time.Time) (changed, jumped bool) {
	last := s.sent.state
	moved := st
	moved.Position = last.Position
	changed = moved != last
	expected := last.Position
	if last.Playing && !last.Waiting {
		expected += now.Sub(s.sent.at).Seconds()
	}
	jumped = math.Abs(st.Position-expected) > jumpSlack
	return changed, jumped
}

// newSongs returns the songs of ids not in the queue last told, and drops
// from the catalog the songs no longer queued.
func (s *Service) newSongs(ids []string) []catalog.Song {
	t := &s.sent
	clear(t.spare)
	var songs []catalog.Song
	for _, id := range ids {
		if _, ok := t.spare[id]; ok {
			continue
		}
		t.spare[id] = struct{}{}
		if _, ok := t.queued[id]; ok {
			continue
		}
		if song, ok := s.cat.Song(id); ok {
			songs = append(songs, song)
		}
	}
	t.queued, t.spare = t.spare, t.queued
	s.keepQueued()
	return songs
}

// keepQueued drops from the catalog the songs not in the queue last told.
func (s *Service) keepQueued() {
	queued := s.sent.queued
	s.cat.KeepSongs(func(id string) bool {
		_, ok := queued[id]
		return ok
	})
	s.sent.catGen = s.cat.Gen()
}

// snapshot is what a new subscriber gets first: everything known.
func (s *Service) snapshot() []api.Frame {
	s.pb.Follow()
	st := s.state()
	ids := s.player.AppendPlayOrder(make([]string, 0, s.player.Len()))
	ev := &api.Event{State: &st, Queue: &api.Queue{IDs: ids}}
	// A copy: the catalog replaces songs in place.
	frames, songs := carry(nil, slices.Clone(s.cat.Songs), queuedSongs)
	ev.Songs = songs
	latest := s.acct.Latest()
	if latest.Status != nil {
		ev.Account = &api.AccountEvent{Account: latest.Status.Account, SignedIn: latest.Status.SignedIn}
	}
	ev.Library = latest.Library
	if latest.Likes != nil {
		ev.Likes = likesEvent(latest.Likes)
		frames, ev.Likes.Songs = carry(frames, ev.Likes.Songs, likedSongs)
	}
	frames = slices.Grow(frames, 1+len(latest.Failed))
	frames = append(frames, api.Frame{Event: ev})
	for _, f := range latest.Failed {
		frames = append(frames, api.Frame{Event: &api.Event{Failed: failedEvent(f)}})
	}
	return frames
}

// carry appends to ahead the frames, made by wrap, of a list of songs too
// long for the event naming them, and returns the songs that event keeps.
func carry(ahead []api.Frame, songs []catalog.Song, wrap func([]catalog.Song) *api.Event) ([]api.Frame, []catalog.Song) {
	if len(songs) <= songsPerFrame {
		return ahead, songs
	}
	size := max(songsPerFrame, (len(songs)+maxSongFrames-1)/maxSongFrames)
	ahead = slices.Grow(ahead, (len(songs)+size-1)/size)
	for chunk := range slices.Chunk(songs, size) {
		ahead = append(ahead, api.Frame{Event: wrap(chunk)})
	}
	return ahead, nil
}

// queuedSongs and likedSongs are events of songs alone, which front-ends
// of every Proto 1 keep for the queue and the likes that follow.
func queuedSongs(songs []catalog.Song) *api.Event { return &api.Event{Songs: songs} }

func likedSongs(songs []catalog.Song) *api.Event {
	return &api.Event{Likes: &api.Likes{Songs: songs}}
}

func likesEvent(l *account.Likes) *api.Likes {
	return &api.Likes{Full: l.Full, IDs: l.IDs, Songs: l.Songs, Added: l.Added, Removed: l.Removed}
}

func failedEvent(f account.Failure) *api.FailedEvent {
	return &api.FailedEvent{Part: api.Part(f.Part) + 1, Err: api.Encode(f.Err)}
}

// accountChanged tells the subscribers what the account reports, after
// the reply of the request that caused it.
func (s *Service) accountChanged(c account.Change) {
	var (
		ev    api.Event
		ahead []api.Frame
	)
	switch {
	case c.Status != nil:
		if c.Status.Switched {
			// The queue was the previous account's; the sign-in fetches
			// the library and likes.
			s.player.Clear()
			s.hasLibrary = true
		}
		ev.Account = &api.AccountEvent{Account: c.Status.Account, SignedIn: c.Status.SignedIn, Switched: c.Status.Switched}
	case c.Library != nil:
		ev.Library = c.Library
	case c.Likes != nil:
		ev.Likes = likesEvent(c.Likes)
		ahead, ev.Likes.Songs = carry(nil, ev.Likes.Songs, likedSongs)
	case c.Failed != nil:
		ev.Failed = failedEvent(*c.Failed)
	}
	s.broadcast(ahead, &ev)
}

// status is the snapshot for `ytm status`.
func (s *Service) status() api.Status {
	st := s.state()
	song, _ := s.cat.Song(st.Current)
	out := api.Status{Version: version.Version, Proto: api.Proto, State: st, Song: song, Length: s.player.Len()}
	// Not Latest, which copies the likes: status bars poll this.
	if st, ok := s.acct.Status(); ok {
		out.SignedIn, out.Account = st.SignedIn, st.Account
	}
	return out
}
