// Package playback drives an audio engine from a player.Player: Follow
// passes on whatever changed in the player (click, shortcut, media key).
// It resolves streams off the UI thread, prefetches the next song near the
// end, resolves a stream that fails once loaded anew (once), and skips
// unplayable songs, stopping after a few in a row. It knows no UI: Follow
// returns when the shown position next moves, and failures become notices.
//
// A Playback belongs to the UI thread, which owns the player and the
// engine. Resolves run on goroutines that call only the Resolver; their
// results and engine events are posted to the UI thread and dropped once
// the song changed.
package playback

import (
	"context"
	"errors"
	"math"
	"strconv"
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
	// tickSlack makes the position redraw land just after its step turns.
	tickSlack = 15 * time.Millisecond
	// focusedStep is the shown position's step, in seconds, while the
	// window has keyboard focus.
	focusedStep = 1
	// blurredStep is the step while unfocused: the window is glanced at,
	// not watched. The Metal driver holds about 250 MB for two seconds
	// after each frame; 5 s steps give it back for 3 s of every 5.
	blurredStep = 5
)

type loadState uint8

const (
	unloaded  loadState = iota
	resolving           // waiting for the resolver
	loading             // the engine has it, not ready yet
	loaded
)

// Playback plays the player's songs.
type Playback struct {
	// Resolver finds songs' streams; NewEngine makes the engine as the
	// first song loads. Without a Resolver nothing plays and the player
	// acts as a mock; Stop, or failing to make an engine, clears it.
	Resolver  stream.Resolver
	NewEngine func(events func(audio.Event)) (audio.Engine, error)
	// Wake runs the prefetch timer's func on the UI thread without a
	// frame, as none is needed and none come while the window is hidden;
	// nil means post.
	Wake func(func())

	cat    *catalog.Catalog
	player *player.Player
	post   func(func())
	engine audio.Engine

	// song is what the engine has or is about to load; state is how far
	// it got.
	song       string
	state      loadState
	isStalled  bool
	stream     stream.Stream
	hasRetried bool
	resolving  job.Job
	// onResolved is pb.resolved, stored once because a method value allocates.
	onResolved func(stream.Stream, error)
	// loads numbers the engine's Loads; events of an older load are dropped.
	loads uint64
	// isPlaying, pos and volume are what the engine last got from the
	// player, to detect changes.
	isPlaying bool
	pos       float64
	volume    float64
	// seconds is the song's length per the engine, 0 until ready.
	seconds float64
	// tickDue is when the shown position's step turns; the next Follow
	// after it reads the engine.
	tickDue time.Time
	// isBlurred: the window lost keyboard focus; isHidden: it is minimized
	// or hidden.
	isBlurred, isHidden bool
	// failures counts songs in a row that could not play.
	failures int
	// notice is the next failure to tell; only the last matters.
	notice notice.Notice

	next          prefetch
	prefetching   job.Job
	prefetchTimer *time.Timer
}

type prefetch struct {
	song       string
	stream     stream.Stream
	isResolved bool
}

// New returns a Playback following p, whose songs cat describes; it is
// silent until given a Resolver. post runs a func on the UI thread and is
// first called once a song plays.
func New(cat *catalog.Catalog, p *player.Player, post func(func())) *Playback {
	pb := &Playback{cat: cat, player: p, post: post}
	pb.onResolved = pb.resolved
	return pb
}

// Follow tells the engine what changed in the player since the last call.
// It returns when to call it again in a frame (as the shown position's
// step turns), or 0 when no frame is needed.
func (pb *Playback) Follow() time.Duration {
	if pb.Resolver == nil {
		return 0
	}
	p := pb.player
	if current := p.Current(); current != pb.song {
		pb.switchSong(current)
	}
	if volume := gain(p); pb.engine != nil && volume != pb.volume {
		pb.volume = volume
		pb.engine.SetVolume(volume)
	}
	if p.Position != pb.pos {
		pb.pos, pb.tickDue = p.Position, time.Time{}
		if pb.state >= loading {
			pb.engine.Seek(p.Position)
		}
	}
	if p.Playing != pb.isPlaying {
		pb.isPlaying, pb.tickDue = p.Playing, time.Time{}
		switch {
		case !p.Playing:
			pb.readPosition()
			if pb.state >= loading {
				pb.engine.Pause()
			}
		case pb.song == "" || pb.state == resolving:
			// Nothing to play, or resolved will play it.
		case pb.state == unloaded || expiring(pb.stream):
			pb.resolve()
		default:
			pb.engine.Play()
		}
	}
	return pb.tick()
}

// tick reads the engine's position once the shown step has turned and
// returns the time until the next turn: every second while focused, on
// multiples of blurredStep while not (so the clock moves in whole steps),
// never while hidden. Song end, errors and Now Playing arrive as engine
// events regardless.
func (pb *Playback) tick() time.Duration {
	if !pb.isAdvancing() {
		pb.tickDue = time.Time{}
		return 0
	}
	now := time.Now()
	if now.Before(pb.tickDue) {
		return pb.tickDue.Sub(now)
	}
	pos := pb.engine.Position()
	if pb.seconds > 0 {
		pos = min(pos, pb.seconds)
	}
	pb.player.Position, pb.pos = pos, pos
	pb.armPrefetch(pos)
	if pb.isHidden {
		// SetShown(true) has the next Follow read it.
		pb.tickDue = time.Time{}
		return 0
	}
	step := float64(focusedStep)
	if pb.isBlurred {
		step = blurredStep
	}
	nextTurn := (math.Floor(pos/step) + 1) * step
	untilTurn := time.Duration((nextTurn-pos)*float64(time.Second)) + tickSlack
	pb.tickDue = now.Add(untilTurn)
	return untilTurn
}

// isAdvancing reports whether the song's position moves on its own.
func (pb *Playback) isAdvancing() bool {
	return pb.player.Playing && pb.state == loaded && !pb.isStalled
}

func (pb *Playback) readPosition() {
	if pb.state == loaded {
		pb.pos = pb.engine.Position()
		pb.player.Position = pb.pos
	}
}

// Resync has the next Follow read where the engine is, whatever the step.
func (pb *Playback) Resync() { pb.tickDue = time.Time{} }

// SetFocused records whether the window has keyboard focus.
func (pb *Playback) SetFocused(focused bool) {
	pb.isBlurred = !focused
	if focused {
		pb.Resync()
	}
}

// SetShown records whether the window is shown, not minimized or hidden.
func (pb *Playback) SetShown(shown bool) {
	pb.isHidden = !shown
	if shown {
		pb.Resync()
	}
}

// IsWatched reports whether the window is focused and shown: only then are
// animations that follow the song worth their frames.
func (pb *Playback) IsWatched() bool { return !pb.isBlurred && !pb.isHidden }

func gain(p *player.Player) float64 {
	if p.Muted {
		return 0
	}
	return p.Volume
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
	pb.isPlaying, pb.pos, pb.tickDue = false, pb.player.Position, time.Time{}
}

// resolve finds the current song's stream, off the UI thread unless
// prefetched.
func (pb *Playback) resolve() {
	pb.resolving.Stop()
	pb.state = resolving
	if s, ok := pb.prefetched(pb.song); ok {
		pb.resolved(s, nil)
		return
	}
	job.Run(&pb.resolving, pb.post, pb.resolveFunc(pb.song), pb.onResolved)
}

// resolveFunc captures the Resolver so the goroutine never reads pb.
func (pb *Playback) resolveFunc(id string) func(context.Context) (stream.Stream, error) {
	resolver := pb.Resolver
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
	pb.stream, pb.state, pb.isStalled, pb.seconds, pb.tickDue = s, loading, false, 0, time.Time{}
	pb.loads++
	pb.engine.SetInfo(audio.Info{
		Title: song.Title, Artist: song.Artist, Album: song.Album,
		Seconds: float64(song.Seconds),
	})
	pb.engine.Load(audio.Media{URL: s.URL, Seconds: s.Seconds, Header: s.Header}, p.Position)
	pb.pos = p.Position
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
	eng, err := pb.NewEngine(func(ev audio.Event) { post(func() { pb.audioEvent(ev) }) })
	if err != nil {
		log.Debug().Err(err).Msg("no audio engine")
		pb.notice = failure("Can’t play audio", err.Error())
		pb.Resolver, pb.state = nil, unloaded
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
	pb.tickDue = time.Time{}
	switch ev.Kind {
	case audio.Ready:
		pb.state, pb.isStalled, pb.failures, pb.hasRetried = loaded, false, 0, false
		pb.seconds = pb.engine.Duration()
	case audio.Buffering:
		pb.isStalled = true
	case audio.Playing:
		pb.isStalled = false
	case audio.Ended:
		pb.songEnded()
	case audio.Failed:
		pb.playFailed(ev.Err)
	}
	pb.Follow()
}

// command applies a media key or Now Playing command to the player.
func (pb *Playback) command(ev audio.Event) {
	// No frame reads the position while the window is hidden, so it may
	// lag; Prev depends on it.
	pb.readPosition()
	p := pb.player
	switch ev.Kind {
	case audio.CommandPlay:
		p.Playing = p.Current() != ""
	case audio.CommandPause:
		p.Playing = false
	case audio.CommandToggle:
		p.TogglePlay()
	case audio.CommandNext:
		p.Next()
	case audio.CommandPrev:
		p.Prev()
	case audio.CommandSeek:
		p.Position = ev.At
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
	pb.pos = p.Position
	pb.engine.Seek(p.Position)
	pb.isPlaying = p.Playing
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
		pb.Resolver.Forget(pb.song)
		pb.resolve()
		return
	}
	title := pb.currentSong().Title
	pb.state, pb.isPlaying = unloaded, false
	pb.failures++
	heading := "Couldn’t play “" + title + "”"
	if errors.Is(err, stream.ErrUnplayable) {
		heading = "“" + title + "” isn’t available"
	}
	if pb.failures >= min(maxSkips, p.Len()) {
		detail := "Playback stopped."
		if pb.failures > 1 {
			detail = "Playback stopped: " + strconv.Itoa(pb.failures) + " songs in a row couldn’t play."
		}
		pb.notice = failure(heading, detail)
		pb.failures, p.Playing = 0, false
		return
	}
	pb.notice = failure(heading, "Skipped to the next song.")
	p.Next()
}

func failure(title, detail string) notice.Notice {
	return notice.Notice{Kind: notice.Failure, Title: title, Detail: detail, Topic: notice.Playback}
}

// TakeNotice returns and clears the failure to tell, if any.
func (pb *Playback) TakeNotice() (notice.Notice, bool) {
	n := pb.notice
	pb.notice = notice.Notice{}
	return n, n.Title != ""
}

// armPrefetch prefetches the next song now if pos (seconds) is near the
// end of this one, else sets the timer for then.
func (pb *Playback) armPrefetch(pos float64) {
	if pb.seconds <= 0 {
		return
	}
	untilPrefetch := pb.seconds - pos - prefetchLead
	if untilPrefetch <= 0 {
		pb.prefetch()
		return
	}
	d := time.Duration(untilPrefetch * float64(time.Second))
	if pb.prefetchTimer != nil {
		pb.prefetchTimer.Reset(d)
		return
	}
	wake := pb.Wake
	if wake == nil {
		wake = pb.post
	}
	pb.prefetchTimer = time.AfterFunc(d, func() { wake(pb.prefetchDue) })
}

// prefetchDue runs when the prefetch timer fires; the song may have
// stalled, been sought or changed since it was set.
func (pb *Playback) prefetchDue() {
	if pb.engine == nil || !pb.isAdvancing() {
		return
	}
	pb.armPrefetch(pb.engine.Position())
}

func (pb *Playback) prefetch() {
	// Called at every step near the end: keep the buffer on the stack.
	var buf [1]string
	upNext := pb.player.AppendUpNext(buf[:0], 1)
	if len(upNext) == 0 || upNext[0] == pb.song || upNext[0] == pb.next.song {
		return
	}
	id := upNext[0]
	pb.next = prefetch{song: id}
	// A failure shows later, when the song plays and resolves again.
	job.Run(&pb.prefetching, pb.post, pb.resolveFunc(id), func(s stream.Stream, err error) {
		if err != nil {
			log.Debug().Str("song", id).Err(err).Msg("prefetch failed")
		}
		pb.next.stream, pb.next.isResolved = s, err == nil
	})
}

// prefetched takes the stream prefetched for id, unless it expires soon.
func (pb *Playback) prefetched(id string) (stream.Stream, bool) {
	next := pb.next
	if next.song != id {
		return stream.Stream{}, false
	}
	pb.prefetching.Stop()
	pb.next = prefetch{}
	return next.stream, next.isResolved && !expiring(next.stream)
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

// Stop cancels resolves and closes the engine as the window closes;
// nothing plays after.
func (pb *Playback) Stop() {
	pb.resolving.Stop()
	pb.prefetching.Stop()
	if pb.prefetchTimer != nil {
		pb.prefetchTimer.Stop()
	}
	if pb.engine != nil {
		pb.engine.Close()
		pb.engine = nil
	}
	pb.Resolver = nil
}
