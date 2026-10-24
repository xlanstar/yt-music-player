// Package playback drives an audio engine from a player.Player: Follow
// passes on whatever changed in the player (a request, a media key) and
// reads where the engine is. It resolves streams off its thread,
// prefetches the next song near the end, resolves a stream that fails once
// loaded anew (once), skips unplayable songs, stopping after a few in a
// row, and has the engine let go of a stream paused for a while. It knows
// no UI and says no words: failures become notices, and the front-ends
// time their frames themselves.
//
// A Playback belongs to the daemon's loop, which owns the player and the
// engine. Resolves run on goroutines that call only the Resolver; their
// results and engine events are posted to the loop and dropped once the
// song changed.
package playback

import (
	"context"
	"errors"
	"time"
	"yt-music-player/internal/audio"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/job"
	"yt-music-player/internal/notice"
	"yt-music-player/internal/player"
	"yt-music-player/internal/stream"

	"github.com/rs/zerolog/log"
)

const (
	// maxSkips is how many songs in a row may fail before playback stops
	// instead of skipping on, e.g. through a queue while offline.
	maxSkips = 5
	// prefetchLead is how many seconds before a song ends the next one's
	// stream is resolved, so it starts without waiting on the network.
	prefetchLead = 20
	// expirySlack: a stream expiring sooner is resolved anew instead of
	// played, as its URL would stop working mid-song.
	expirySlack = time.Minute
	// releaseAfter is how long the engine keeps a stream paused (about 2 MB:
	// its download, decoder and output) before Release; Play then loads it
	// again, a request away.
	releaseAfter = time.Minute
)

type loadState uint8

const (
	unloaded  loadState = iota
	resolving           // waiting for the resolver
	released            // the engine let go of it, paused: Play loads it again
	loading             // the engine has it, not ready yet
	loaded
)

// Playback plays the player's songs.
type Playback struct {
	// resolver is Options.Resolver until Stop or a failure to make the
	// engine clears it: nothing plays after.
	resolver  stream.Resolver
	newEngine func(events func(audio.Event)) (audio.Engine, error)
	// released is Options.Released, or nil.
	released func()

	cat    *catalog.Catalog
	player *player.Player
	post   func(func())
	engine audio.Engine

	// song is the ID the engine has or is about to load.
	song      string
	stream    stream.Stream
	resolving job.Job
	// onResolved is pb.resolved, stored once because a method value allocates.
	onResolved func(stream.Stream, error)
	// loads numbers the engine's Loads; events of an older load are dropped.
	loads uint64
	// pos and volume cache the last values sent to the engine to detect changes.
	pos    float64
	volume float64
	// seconds is the song's length per the engine, 0 until ready.
	seconds float64
	// failures counts songs in a row that could not play.
	failures int
	// notice is the next failure to tell; only the last matters.
	notice notice.Notice

	prefetch prefetcher
	release  releaser
	// state tracks how far loading song has progressed.
	state      loadState
	isStalled  bool
	hasRetried bool
	// isPlaying caches the last playing state sent to the engine to detect changes.
	isPlaying bool
}

// prefetcher resolves the song up next near the end of this one.
type prefetcher struct {
	next  prefetch
	job   job.Job
	timer *time.Timer
}

type prefetch struct {
	song       string
	stream     stream.Stream
	isResolved bool
}

// releaser has the engine let go of a stream paused for releaseAfter.
type releaser struct {
	timer *time.Timer
	// holds includes the current or previous stream still held by the engine.
	// isArmed means timer runs for that paused stream.
	holds, isArmed bool
}

// Options are what a Playback plays with.
type Options struct {
	// Resolver finds streams; without one the player is a mock: nothing
	// plays.
	Resolver stream.Resolver
	// NewEngine makes the engine as the first song loads.
	NewEngine func(events func(audio.Event)) (audio.Engine, error)
	// Released, if set, runs on the loop once the engine let go of a
	// stream, whose memory is garbage then (about 2 MB).
	Released func()
}

// New returns a Playback following p, whose songs cat describes. post runs
// a func on the loop and is first called once a song plays.
func New(cat *catalog.Catalog, p *player.Player, post func(func()), opts Options) *Playback {
	pb := &Playback{
		cat:       cat,
		player:    p,
		post:      post,
		resolver:  opts.Resolver,
		newEngine: opts.NewEngine,
		released:  opts.Released,
	}
	pb.onResolved = pb.resolved
	return pb
}

// Follow tells the engine what changed in the player since the last call,
// and moves the player's position to where the engine is.
func (pb *Playback) Follow() {
	if pb.resolver == nil {
		return
	}
	p := pb.player
	if current := p.Current(); current != pb.song {
		pb.switchSong(current)
	}
	if volume := gain(p); pb.engine != nil && volume != pb.volume {
		pb.volume = volume
		pb.engine.SetVolume(volume)
	}
	if p.Position() != pb.pos {
		pb.pos = p.Position()
		if pb.state >= released {
			pb.engine.Seek(p.Position())
		}
	}
	if p.IsPlaying() != pb.isPlaying {
		pb.isPlaying = p.IsPlaying()
		switch {
		case !p.IsPlaying():
			pb.readPosition()
			if pb.state >= loading {
				pb.engine.Pause()
			}
		case pb.song == "" || pb.state == resolving:
			// Nothing to play, or resolved will play it.
		case pb.state < loading || expiring(pb.stream):
			pb.resolve()
		default:
			pb.engine.Play()
		}
	}
	pb.follow()
	pb.armRelease()
}

// follow reads the engine's position while the song moves on its own.
// Song end, errors and Now Playing arrive as engine events regardless.
func (pb *Playback) follow() {
	if !pb.isAdvancing() {
		return
	}
	pos := pb.engine.Position()
	if pb.seconds > 0 {
		pos = min(pos, pb.seconds)
	}
	pb.player.Seek(pos)
	pb.pos = pos
	pb.armPrefetch(pos)
}

// IsWaiting reports whether the player plays a song whose position stands
// still: its stream resolving or loading, or stalled; always so without a
// Resolver.
func (pb *Playback) IsWaiting() bool {
	return pb.player.IsPlaying() && pb.player.Current() != "" && !pb.isAdvancing()
}

// isAdvancing reports whether the song's position moves on its own.
func (pb *Playback) isAdvancing() bool {
	return pb.player.IsPlaying() && pb.state == loaded && !pb.isStalled
}

func (pb *Playback) readPosition() {
	if pb.state == loaded {
		pb.pos = pb.engine.Position()
		pb.player.Seek(pb.pos)
	}
}

func gain(p *player.Player) float64 {
	if p.IsMuted() {
		return 0
	}
	return p.Volume()
}

func expiring(s stream.Stream) bool {
	return !s.Expires.IsZero() && time.Until(s.Expires) < expirySlack
}

// currentSong is the zero Song when the queue is empty.
func (pb *Playback) currentSong() catalog.Song {
	s, _ := pb.cat.Song(pb.player.Current())
	return s
}

// switchSong drops the engine's song for id, which loads once it plays.
func (pb *Playback) switchSong(id string) {
	pb.resolving.Stop()
	if pb.state >= loading {
		pb.engine.Pause()
	}
	pb.song, pb.state, pb.stream, pb.seconds = id, unloaded, stream.Stream{}, 0
	pb.isStalled, pb.hasRetried = false, false
	// Not playing yet, so Follow loads it if the player plays.
	pb.isPlaying, pb.pos = false, pb.player.Position()
}

// resolve finds the current song's stream: the one released if it still
// works, else off the loop unless prefetched.
func (pb *Playback) resolve() {
	pb.resolving.Stop()
	isReleased := pb.state == released
	pb.state = resolving
	if isReleased && !expiring(pb.stream) {
		pb.resolved(pb.stream, nil)
		return
	}
	if s, ok := pb.prefetch.take(pb.song); ok {
		pb.resolved(s, nil)
		return
	}
	pb.resolving.Run(pb.post, pb.resolveFunc(pb.song), pb.onResolved)
}

// resolveFunc captures the Resolver so the goroutine never reads pb.
func (pb *Playback) resolveFunc(id string) func(context.Context) (stream.Stream, error) {
	resolver := pb.resolver
	return func(ctx context.Context) (stream.Stream, error) { return resolver.Resolve(ctx, id) }
}

func (pb *Playback) resolved(s stream.Stream, err error) {
	if err != nil {
		pb.playFailed(err)
		return
	}
	if !pb.startEngine() {
		return
	}
	p, song := pb.player, pb.currentSong()
	pb.stream, pb.state, pb.isStalled, pb.seconds = s, loading, false, 0
	pb.loads++
	pb.engine.SetInfo(audio.Info{
		Title: song.Title, Artist: song.Artist, Album: song.Album,
		Seconds: float64(song.Seconds),
	})
	pb.engine.Load(audio.Media{URL: s.URL, Seconds: s.Seconds, Header: s.Header}, p.Position())
	pb.pos, pb.release.holds = p.Position(), true
	if pb.isPlaying {
		pb.engine.Play()
	}
}

func (pb *Playback) startEngine() bool {
	if pb.engine != nil {
		return true
	}
	// Events come from any thread: capture post rather than read pb there.
	post := pb.post
	eng, err := pb.newEngine(func(ev audio.Event) { post(func() { pb.audioEvent(ev) }) })
	if err != nil {
		log.Debug().Err(err).Msg("no audio engine")
		pb.notice = failure(notice.NoAudio, notice.Reason, err.Error())
		pb.resolver, pb.state = nil, unloaded
		return false
	}
	pb.engine, pb.volume = eng, gain(pb.player)
	eng.SetVolume(pb.volume)
	return true
}

func (pb *Playback) audioEvent(ev audio.Event) {
	if pb.engine == nil {
		return
	}
	if ev.IsCommand() {
		pb.command(ev)
		return
	}
	if ev.Load != pb.loads || pb.state < loading {
		return
	}
	switch ev.Kind {
	case audio.StreamReady:
		pb.state, pb.isStalled, pb.failures, pb.hasRetried = loaded, false, 0, false
		pb.seconds = pb.engine.Duration()
	case audio.StreamBuffering:
		pb.isStalled = true
	case audio.StreamPlaying:
		pb.isStalled = false
	case audio.StreamEnded:
		pb.songEnded()
	case audio.StreamFailed:
		pb.playFailed(ev.Err)
	}
	pb.Follow()
}

// command applies a media key or Now Playing command to the player.
func (pb *Playback) command(ev audio.Event) {
	// Catch up first, as the daemon does before a request: a request in
	// this batch may have changed the song, and Prev reads the position.
	pb.Follow()
	p := pb.player
	switch ev.Kind {
	case audio.CommandPlay:
		p.Resume()
	case audio.CommandPause:
		p.Pause()
	case audio.CommandToggle:
		p.TogglePlay()
	case audio.CommandNext:
		p.Next()
	case audio.CommandPrev:
		p.Prev()
	case audio.CommandSeek:
		p.Seek(ev.At)
	}
	pb.Follow()
}

func (pb *Playback) songEnded() {
	p := pb.player
	p.Finished()
	if p.Current() != pb.song {
		return
	}
	// RepeatOne, or a one-song queue repeating: the engine paused at the
	// end while the player still plays.
	pb.pos = p.Position()
	pb.engine.Seek(p.Position())
	pb.isPlaying = p.IsPlaying()
	if pb.isPlaying {
		pb.engine.Play()
	}
}

// playFailed skips an unplayable song and stops after too many in a row.
// A stream that fails once loaded (expired, or its session lost its token)
// is resolved anew, once.
func (pb *Playback) playFailed(err error) {
	p := pb.player
	canRetry := pb.state >= loading && !pb.hasRetried
	retry := canRetry && (expiring(pb.stream) || !errors.Is(err, stream.ErrUnplayable))
	log.Debug().Str("song", pb.song).Bool("retry", retry).Err(err).Msg("play failed")
	if retry {
		pb.hasRetried = true
		pb.resolver.Forget(pb.song)
		pb.resolve()
		return
	}
	title := pb.currentSong().Title
	pb.state, pb.isPlaying = unloaded, false
	pb.failures++
	heading := notice.PlayFailed
	if errors.Is(err, stream.ErrUnplayable) {
		heading = notice.Unplayable
	}
	if pb.failures >= min(maxSkips, p.Len()) {
		pb.notice = failure(heading, notice.PlaybackStopped, title)
		if pb.failures > 1 {
			pb.notice.Detail, pb.notice.N = notice.PlaybackStoppedAfter, pb.failures
		}
		pb.failures = 0
		p.Pause()
		return
	}
	pb.notice = failure(heading, notice.Skipped, title)
	p.Skip()
}

func failure(title, detail notice.Code, args ...string) notice.Notice {
	return notice.Notice{Kind: notice.KindFailure, Title: title, Detail: detail, Args: args, Topic: notice.TopicPlayback}
}

// TakeNotice returns and clears the failure to tell, if any.
func (pb *Playback) TakeNotice() (notice.Notice, bool) {
	n := pb.notice
	pb.notice = notice.Notice{}
	return n, n.Title != 0
}

// armPrefetch prefetches the next song now if pos (seconds) is near the
// end of this one, else sets the timer for then.
func (pb *Playback) armPrefetch(pos float64) {
	if pb.seconds <= 0 {
		return
	}
	untilPrefetch := pb.seconds - pos - prefetchLead
	if untilPrefetch <= 0 {
		pb.startPrefetch()
		return
	}
	d := time.Duration(untilPrefetch * float64(time.Second))
	if pb.prefetch.timer != nil {
		pb.prefetch.timer.Reset(d)
		return
	}
	post := pb.post
	pb.prefetch.timer = time.AfterFunc(d, func() { post(pb.prefetchDue) })
}

// prefetchDue runs when the prefetch timer fires; the song may have
// stalled, been sought or changed since it was set.
func (pb *Playback) prefetchDue() {
	if pb.engine == nil || !pb.isAdvancing() {
		return
	}
	pb.armPrefetch(pb.engine.Position())
}

func (pb *Playback) startPrefetch() {
	// Called at every step near the end: keep the buffer on the stack.
	var buf [1]string
	upNext := pb.player.AppendUpNext(buf[:0], 1)
	f := &pb.prefetch
	if len(upNext) == 0 || upNext[0] == pb.song || upNext[0] == f.next.song {
		return
	}
	id := upNext[0]
	f.next = prefetch{song: id}
	// A failure shows later, when the song plays and resolves again.
	f.job.Run(pb.post, pb.resolveFunc(id), func(s stream.Stream, err error) {
		if err != nil {
			log.Debug().Str("song", id).Err(err).Msg("prefetch failed")
		}
		f.next.stream, f.next.isResolved = s, err == nil
	})
}

// take takes the stream prefetched for id, unless it expires soon.
func (f *prefetcher) take(id string) (stream.Stream, bool) {
	next := f.next
	if next.song != id {
		return stream.Stream{}, false
	}
	f.job.Stop()
	f.next = prefetch{}
	return next.stream, next.isResolved && !expiring(next.stream)
}

func (f *prefetcher) stop() {
	f.job.Stop()
	if f.timer != nil {
		f.timer.Stop()
	}
}

// armRelease runs the release timer while the engine holds a paused
// stream: from the pause, not pushed back by what else the loop does.
func (pb *Playback) armRelease() {
	r := &pb.release
	isPaused := r.holds && !pb.isPlaying
	if isPaused == r.isArmed {
		return
	}
	r.isArmed = isPaused
	switch {
	case !isPaused:
		r.timer.Stop()
	case r.timer == nil:
		post := pb.post
		r.timer = time.AfterFunc(releaseAfter, func() { post(pb.releaseDue) })
	default:
		r.timer.Reset(releaseAfter)
	}
}

// releaseDue runs when the release timer fires: the stream may play again
// since it was set.
func (pb *Playback) releaseDue() {
	if !pb.release.isArmed || pb.engine == nil {
		return
	}
	pb.release.isArmed, pb.release.holds = false, false
	pb.engine.Release()
	if pb.state >= loading {
		pb.state, pb.isStalled = released, false
	}
	if pb.released != nil {
		pb.released()
	}
}

func (r *releaser) stop() {
	if r.timer != nil {
		r.timer.Stop()
	}
}

// SongLength returns s's length in seconds, from its stream once it
// plays, which may know better than the catalog.
func (pb *Playback) SongLength(s catalog.Song) float64 {
	if pb.song == s.ID && s.ID != "" {
		if pb.seconds > 0 {
			return pb.seconds
		}
		if s.Seconds == 0 && pb.stream.Seconds > 0 {
			return pb.stream.Seconds
		}
	}
	return float64(s.Seconds)
}

// Stop cancels resolves and closes the engine as the daemon stops;
// nothing plays after.
func (pb *Playback) Stop() {
	pb.resolving.Stop()
	pb.prefetch.stop()
	pb.release.stop()
	if pb.engine != nil {
		pb.engine.Close()
		pb.engine = nil
	}
	pb.resolver = nil
}
