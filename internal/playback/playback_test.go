package playback

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"yt-music-player/internal/audio"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/notice"
	"yt-music-player/internal/player"
	"yt-music-player/internal/stream"
)

// posts stands in for the UI thread: playback queues results and the test
// runs them on its own goroutine.
type posts chan func()

// instantResolver answers at once with a stream named after the song, or
// with err.
type instantResolver struct {
	mu      sync.Mutex
	asked   []string
	err     error
	expires time.Time
	forgot  []string
}

func (r *instantResolver) Resolve(_ context.Context, id string) (stream.Stream, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.asked = append(r.asked, id)
	if r.err != nil {
		return stream.Stream{}, r.err
	}
	return stream.Stream{URL: "https://audio/" + id, Expires: r.expires}, nil
}

func (r *instantResolver) Forget(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.forgot = append(r.forgot, id)
}

func (r *instantResolver) Asked() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.asked)
}

// fixture is a Playback on a fake engine following a player over the
// placeholder's songs, part-way into the first, as the app starts.
type fixture struct {
	pb     *Playback
	player *player.Player
	cat    *catalog.Catalog
	f      *audio.Fake
	q      posts
}

func newFixture(res stream.Resolver) *fixture {
	cat := catalog.Placeholder()
	x := &fixture{cat: cat, player: player.New(cat.SongIDs()), q: make(posts, 64)}
	x.player.Position = 84
	var events func(audio.Event)
	x.f = audio.NewFake(func(ev audio.Event) { events(ev) })
	x.pb = New(x.cat, x.player, func(fn func()) { x.q <- fn })
	x.pb.Resolver = res
	x.pb.NewEngine = func(ev func(audio.Event)) (audio.Engine, error) {
		events = ev
		return x.f, nil
	}
	return x
}

func (x *fixture) run(t testing.TB) {
	t.Helper()
	select {
	case fn := <-x.q:
		fn()
	case <-time.After(2 * time.Second):
		t.Fatal("nothing posted")
	}
}

func (x *fixture) noPost(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case <-x.q:
		t.Fatal("posted, want nothing")
	case <-time.After(d):
	}
}

func (x *fixture) wantCalls(t *testing.T, want ...string) {
	t.Helper()
	if got := x.f.TakeCalls(); !slices.Equal(got, want) {
		t.Errorf("engine calls %q, want %q", got, want)
	}
}

// startPlaying plays the current song from the start until it is ready.
func (x *fixture) startPlaying(t testing.TB) {
	t.Helper()
	x.player.Position = 0
	x.player.Playing = true
	x.pb.Follow()
	x.run(t)
	x.f.Dur = 200
	x.f.Emit(audio.Ready)
	x.run(t)
	x.f.TakeCalls()
}

// turn mimics the frame Follow asked for: the step turns and the position
// is read anew.
func (x *fixture) turn(pos float64) time.Duration {
	x.f.Pos = pos
	x.pb.Resync()
	return x.pb.Follow()
}

func TestLoadsAndPlays(t *testing.T) {
	res := &instantResolver{}
	x := newFixture(res)
	first := x.player.Current()
	x.pb.Follow()
	if len(res.Asked()) != 0 {
		t.Fatalf("resolved %q while paused", res.Asked())
	}
	x.player.Playing = true
	x.pb.Follow()
	x.run(t)
	// The mock starts part-way into the song.
	x.wantCalls(t, "volume 0.7", "load https://audio/"+first+" at 84", "play")
	if x.f.Info.Title == "" || x.f.Info.Seconds == 0 {
		t.Errorf("info %+v", x.f.Info)
	}
	if d := x.pb.Follow(); d != 0 {
		t.Errorf("ticks every %v before the stream is ready", d)
	}
	x.f.Dur, x.f.Pos = 201.5, 84.25
	x.f.Emit(audio.Ready)
	x.run(t)
	song, _ := x.cat.Song(first)
	if got := x.pb.SongLength(song); got != 201.5 {
		t.Errorf("length %v, want the engine's 201.5", got)
	}
	if d := x.pb.Follow(); d < 700*time.Millisecond || d > 800*time.Millisecond {
		t.Errorf("next tick in %v, want as 84.25 turns 85", d)
	}

	x.player.Next()
	x.pb.Follow()
	x.run(t)
	second := x.player.Current()
	x.wantCalls(t, "pause", "load https://audio/"+second+" at 0", "play")
	if got := res.Asked(); !slices.Equal(got, []string{first, second}) {
		t.Errorf("resolved %q", got)
	}
	// The song left is as long as the catalog says.
	if got := x.pb.SongLength(song); got != float64(song.Seconds) {
		t.Errorf("length of the song left %v, want the catalog's %d", got, song.Seconds)
	}
}

func TestSongLength(t *testing.T) {
	x := newFixture(&instantResolver{})
	x.startPlaying(t)
	song, _ := x.cat.Song(x.player.Current())
	if got := x.pb.SongLength(song); got != 200 {
		t.Errorf("ready: length %v, want the engine's 200", got)
	}
	// Before the engine knows, a song the catalog has no length for is
	// as long as its stream says.
	x.pb.seconds, x.pb.stream.Seconds = 0, 187
	if got := x.pb.SongLength(song); got != float64(song.Seconds) {
		t.Errorf("loading: length %v, want the catalog's %d", got, song.Seconds)
	}
	song.Seconds = 0
	if got := x.pb.SongLength(song); got != 187 {
		t.Errorf("loading, no length in the catalog: %v, want the stream's 187", got)
	}
	if got := x.pb.SongLength(catalog.Song{}); got != 0 {
		t.Errorf("no song: length %v", got)
	}
}

// gatedResolver answers only when the test says, ignoring the context, as
// a source may.
type gatedResolver struct{ reqs chan gatedReq }

type gatedReq struct {
	id    string
	reply chan stream.Stream
}

func (gatedResolver) Forget(string) {}

func (r gatedResolver) Resolve(_ context.Context, id string) (stream.Stream, error) {
	req := gatedReq{id, make(chan stream.Stream, 1)}
	r.reqs <- req
	return <-req.reply, nil
}

func TestStaleResolveIsDropped(t *testing.T) {
	res := gatedResolver{make(chan gatedReq, 4)}
	x := newFixture(res)
	x.player.Playing = true
	x.pb.Follow()
	first := <-res.reqs
	x.player.Next()
	x.pb.Follow()
	second := <-res.reqs
	second.reply <- stream.Stream{URL: "https://audio/" + second.id}
	x.run(t)
	first.reply <- stream.Stream{URL: "https://audio/" + first.id}
	// Canceled, the first resolve posts nothing.
	x.noPost(t, 50*time.Millisecond)
	if got := x.f.TakeCalls(); slices.ContainsFunc(got, func(c string) bool { return strings.Contains(c, first.id) }) {
		t.Errorf("a stale stream loaded: %q", got)
	}
	if !strings.HasSuffix(x.f.Media.URL, second.id) {
		t.Errorf("loaded %q, want %q", x.f.Media.URL, second.id)
	}
}

func TestEndedMovesOnOrRepeats(t *testing.T) {
	x := newFixture(&instantResolver{})
	x.startPlaying(t)
	first := x.player.Current()
	x.f.Emit(audio.Ended)
	x.run(t)
	x.run(t)
	if x.player.Current() == first || !x.player.Playing {
		t.Fatalf("after the end at %q (playing %v)", x.player.Current(), x.player.Playing)
	}
	// The engine already paused at the end; pausing again is harmless.
	x.wantCalls(t, "pause", "load https://audio/"+x.player.Current()+" at 0", "play")

	x.f.Emit(audio.Ready)
	x.run(t)
	x.player.Repeat = player.RepeatOne
	x.f.Pos = 200
	x.f.Emit(audio.Ended)
	x.run(t)
	x.wantCalls(t, "seek 0", "play")
	if x.player.Position != 0 || !x.player.Playing {
		t.Errorf("repeat one at %v playing %v", x.player.Position, x.player.Playing)
	}
	// Events of a stream replaced since are stale.
	x.player.Repeat = player.RepeatOff
	x.pb.audioEvent(audio.Event{Kind: audio.Ended, Load: x.f.Loads - 1})
	x.wantCalls(t)
}

func TestEndOfQueueStops(t *testing.T) {
	x := newFixture(&instantResolver{})
	x.player.PlayAll([]string{x.cat.SongIDs()[0]})
	x.startPlaying(t)
	x.f.Emit(audio.Ended)
	x.run(t)
	if x.player.Playing {
		t.Error("still playing past the end of the queue")
	}
	x.wantCalls(t, "seek 0")

	x.player.Repeat = player.RepeatAll
	x.player.Playing = true
	x.pb.Follow()
	x.f.TakeCalls()
	x.f.Emit(audio.Ended)
	x.run(t)
	x.wantCalls(t, "seek 0", "play")
}

// failAll runs posted failures, following each, until none comes.
func (x *fixture) failAll() {
	for {
		select {
		case fn := <-x.q:
			fn()
			x.pb.Follow()
			continue
		case <-time.After(200 * time.Millisecond):
		}
		return
	}
}

func TestFailuresSkipThenStop(t *testing.T) {
	res := &instantResolver{err: stream.ErrUnplayable}
	x := newFixture(res)
	x.player.Repeat = player.RepeatAll
	x.player.Playing = true
	x.pb.Follow()
	x.failAll()
	if n := len(res.Asked()); n != maxSkips {
		t.Errorf("tried %d songs, want %d", n, maxSkips)
	}
	last, _ := x.cat.Song(x.player.Current())
	want := notice.Notice{
		Kind: notice.Failure, Title: notice.Unplayable, Detail: notice.PlaybackStoppedAfter,
		Args: []string{last.Title}, N: maxSkips, Topic: notice.Playback,
	}
	if n, ok := x.pb.TakeNotice(); x.player.Playing || !ok || !reflect.DeepEqual(n, want) {
		t.Errorf("playing %v, notice %+v, want %+v", x.player.Playing, n, want)
	}
	if _, ok := x.pb.TakeNotice(); ok {
		t.Error("a notice told twice")
	}
	if len(x.f.Calls) != 0 {
		t.Errorf("engine calls %q for unplayable songs", x.f.Calls)
	}

	// A stream failing as it plays resolves anew once, then skips too,
	// and a song that plays clears the count.
	res.err = nil
	x.startPlaying(t)
	cur := x.player.Current()
	song, _ := x.cat.Song(cur)
	x.f.Fail(errors.New("decoder broke"))
	x.run(t)
	x.run(t)
	x.f.Fail(errors.New("decoder broke"))
	x.run(t)
	n, _ := x.pb.TakeNotice()
	isSkipNotice := n.Title == notice.PlayFailed && n.Detail == notice.Skipped && slices.Equal(n.Args, []string{song.Title})
	if x.player.Current() == cur || !isSkipNotice {
		t.Errorf("a failed stream left %q, notice %+v", x.player.Current(), n)
	}
	x.run(t)
	x.f.Emit(audio.Ready)
	x.run(t)
	if x.pb.failures != 0 {
		t.Errorf("%d failures after a song played", x.pb.failures)
	}
}

func TestFailuresStopWithinTheQueue(t *testing.T) {
	res := &instantResolver{err: stream.ErrUnplayable}
	x := newFixture(res)
	ids := x.cat.SongIDs()[:3]
	x.player.PlayAll(ids)
	x.player.Repeat = player.RepeatAll
	x.player.Playing = true
	x.pb.Follow()
	x.failAll()
	// A queue shorter than maxSkips stops once each song failed.
	if n := len(res.Asked()); n != len(ids) {
		t.Errorf("tried %d songs, want %d", n, len(ids))
	}
	if n, _ := x.pb.TakeNotice(); x.player.Playing || n.Detail != notice.PlaybackStoppedAfter || n.N != 3 {
		t.Errorf("playing %v, notice %+v", x.player.Playing, n)
	}
}

func TestEngineFails(t *testing.T) {
	x := newFixture(&instantResolver{})
	x.pb.NewEngine = func(func(audio.Event)) (audio.Engine, error) { return nil, errNoAudio }
	x.player.Playing = true
	x.pb.Follow()
	x.run(t)
	if n, ok := x.pb.TakeNotice(); !ok || n.Title != notice.NoAudio || !slices.Equal(n.Args, []string{errNoAudio.Error()}) {
		t.Errorf("notice %+v", n)
	}
	// The player goes on as a mock.
	x.player.Next()
	if d := x.pb.Follow(); d != 0 || len(x.q) != 0 {
		t.Errorf("without an engine: a tick in %v, %d posts", d, len(x.q))
	}
}

func TestSeekAndVolumeAreForwarded(t *testing.T) {
	x := newFixture(&instantResolver{})
	x.startPlaying(t)
	x.player.Position = 42
	x.player.Volume = 0.3
	x.pb.Follow()
	x.wantCalls(t, "volume 0.3", "seek 42")
	x.player.ToggleMute()
	x.pb.Follow()
	x.wantCalls(t, "volume 0")
	x.player.ToggleMute()
	x.pb.Follow()
	x.wantCalls(t, "volume 0.3")
	x.pb.audioEvent(audio.Event{Kind: audio.CommandSeek, At: 100})
	x.wantCalls(t, "seek 100")
}

func TestNoTicksWhilePaused(t *testing.T) {
	x := newFixture(&instantResolver{})
	x.startPlaying(t)
	if d := x.turn(10.5); d <= 0 || x.player.Position != 10.5 {
		t.Fatalf("playing: tick in %v, at %v", d, x.player.Position)
	}
	x.player.TogglePlay()
	if d := x.pb.Follow(); d != 0 {
		t.Errorf("paused, a tick in %v", d)
	}
	x.wantCalls(t, "pause")
	x.f.Pos = 30
	if d := x.pb.Follow(); d != 0 || x.player.Position != 10.5 {
		t.Errorf("paused, tick in %v, at %v", d, x.player.Position)
	}
	x.noPost(t, 1200*time.Millisecond)

	// Stalled, the position stands still too.
	x.player.TogglePlay()
	x.pb.Follow()
	x.f.Emit(audio.Buffering)
	x.run(t)
	if d := x.pb.Follow(); d != 0 {
		t.Errorf("buffering, a tick in %v", d)
	}
	x.f.Emit(audio.Playing)
	x.run(t)
	if d := x.pb.Follow(); d <= 0 {
		t.Error("no tick once playing on")
	}
}

func TestExpiredStreamResolvesAnew(t *testing.T) {
	res := &instantResolver{expires: time.Now().Add(30 * time.Second)}
	x := newFixture(res)
	x.startPlaying(t)
	cur := x.player.Current()
	x.f.Pos = 50
	x.player.TogglePlay()
	x.pb.Follow()
	x.player.TogglePlay()
	x.pb.Follow()
	x.run(t)
	x.wantCalls(t, "pause", "load https://audio/"+cur+" at 50", "play")

	// A stream failing once expired resolves anew, once, then skips.
	x.f.Emit(audio.Ready)
	x.run(t)
	x.f.Fail(errors.New("403"))
	x.run(t)
	x.run(t)
	if n, ok := x.pb.TakeNotice(); x.player.Current() != cur || ok {
		t.Errorf("an expired stream skipped to %q, notice %+v", x.player.Current(), n)
	}
	x.f.Fail(errors.New("403"))
	x.run(t)
	x.run(t)
	if x.player.Current() == cur {
		t.Error("a stream failing again did not skip")
	}
	if n := len(res.Asked()); n != 4 {
		t.Errorf("resolved %d times, want 4", n)
	}
	res.mu.Lock()
	defer res.mu.Unlock()
	if !slices.Equal(res.forgot, []string{cur}) {
		t.Errorf("forgot %v, want the failed song once", res.forgot)
	}
}

func TestFailedStreamResolvesAnewOnce(t *testing.T) {
	// Fresh, but its session lost its token mid-song: googlevideo 403s.
	res := &instantResolver{expires: time.Now().Add(5 * time.Hour)}
	x := newFixture(res)
	x.startPlaying(t)
	cur := x.player.Current()
	x.f.Fail(errors.New("403"))
	x.run(t)
	x.run(t)
	if x.player.Current() != cur {
		t.Fatalf("a failed stream skipped to %q before resolving anew", x.player.Current())
	}
	x.f.Fail(errors.New("403"))
	x.run(t)
	x.run(t)
	if x.player.Current() == cur {
		t.Error("a stream failing again did not skip")
	}
}

func TestPrefetchesTheNextSong(t *testing.T) {
	res := &instantResolver{}
	x := newFixture(res)
	x.startPlaying(t)
	next := x.player.AppendUpNext(nil, 1)[0]
	x.turn(150)
	x.noPost(t, 50*time.Millisecond)
	x.turn(185)
	x.run(t)
	if got := res.Asked(); got[len(got)-1] != next {
		t.Fatalf("resolved %q, want %q prefetched", got, next)
	}
	asked := len(res.Asked())
	x.f.Emit(audio.Ended)
	x.run(t)
	// Prefetched, the next song loads with no post.
	x.wantCalls(t, "pause", "load https://audio/"+next+" at 0", "play")
	if len(res.Asked()) != asked {
		t.Error("the prefetched song resolved again")
	}
}

func TestExpiringPrefetchResolvesAnew(t *testing.T) {
	res := &instantResolver{expires: time.Now().Add(30 * time.Second)}
	x := newFixture(res)
	x.startPlaying(t)
	next := x.player.AppendUpNext(nil, 1)[0]
	x.turn(185)
	x.run(t)
	if !x.pb.next.isResolved {
		t.Fatal("the prefetched stream was not kept")
	}
	asked := len(res.Asked())
	x.f.Emit(audio.Ended)
	x.run(t)
	x.run(t)
	if got := res.Asked(); len(got) != asked+1 || got[asked] != next {
		t.Errorf("resolved %q, want %q anew", got, next)
	}
}

func TestSystemCommands(t *testing.T) {
	x := newFixture(&instantResolver{})
	x.startPlaying(t)
	first := x.player.Current()
	x.f.Command(audio.CommandToggle, 0)
	x.run(t)
	if x.player.Playing {
		t.Error("toggle did not pause")
	}
	x.f.Command(audio.CommandPlay, 0)
	x.run(t)
	if !x.player.Playing {
		t.Error("play did not play")
	}
	x.wantCalls(t, "pause", "play")
	// Prev 30 s into the song restarts it, reading where the engine is.
	x.f.Pos = 30
	x.pb.audioEvent(audio.Event{Kind: audio.CommandPrev})
	if x.player.Current() != first || x.player.Position != 0 {
		t.Errorf("prev 30 s in went to %q at %v", x.player.Current(), x.player.Position)
	}
	x.pb.audioEvent(audio.Event{Kind: audio.CommandNext})
	x.run(t)
	if x.player.Current() == first {
		t.Error("next stayed")
	}
}

func TestNoPlaybackWithoutResolver(t *testing.T) {
	x := newFixture(nil)
	x.pb.NewEngine = func(func(audio.Event)) (audio.Engine, error) {
		t.Error("made an engine with no resolver")
		return nil, errNoAudio
	}
	x.player.Playing = true
	if d := x.pb.Follow(); d != 0 || len(x.q) != 0 || x.pb.state != unloaded {
		t.Errorf("no resolver: a tick in %v, %d posts, state %v", d, len(x.q), x.pb.state)
	}
}

func TestStop(t *testing.T) {
	x := newFixture(&instantResolver{})
	x.startPlaying(t)
	x.pb.Stop()
	x.wantCalls(t, "close")
	x.player.Next()
	if d := x.pb.Follow(); d != 0 || len(x.q) != 0 {
		t.Errorf("stopped: a tick in %v, %d posts", d, len(x.q))
	}
}

func tickIn(t *testing.T, d, lo, hi time.Duration) {
	t.Helper()
	if d < lo || d > hi {
		t.Errorf("next frame in %v, want %v to %v", d, lo, hi)
	}
}

func TestTicksFollowTheWindow(t *testing.T) {
	x := newFixture(&instantResolver{})
	x.startPlaying(t)

	// Focused: a frame as each second turns.
	tickIn(t, x.turn(62.3), 700*time.Millisecond, 750*time.Millisecond)

	// Unfocused: at the next multiple of 5 s, then every 5 s, so that the
	// clock moves by whole steps.
	x.pb.SetFocused(false)
	if d := x.pb.Follow(); d > 750*time.Millisecond {
		t.Errorf("losing the focus moved the frame due to %v", d)
	}
	tickIn(t, x.turn(63.02), 1980*time.Millisecond, 2*time.Second)
	tickIn(t, x.turn(65.01), 4990*time.Millisecond, 5010*time.Millisecond)
	if x.player.Position != 65.01 {
		t.Errorf("shows %v, want 65.01", x.player.Position)
	}
	x.f.Pos = 67
	if d := x.pb.Follow(); d <= 0 || x.player.Position != 65.01 {
		t.Errorf("between steps: frame in %v, at %v", d, x.player.Position)
	}

	// Refocused: the next frame reads the engine, then each second again.
	x.pb.SetFocused(true)
	x.f.Pos = 67.4
	tickIn(t, x.pb.Follow(), 600*time.Millisecond, 650*time.Millisecond)
	if x.player.Position != 67.4 {
		t.Errorf("refocused at %v, want the engine's 67.4", x.player.Position)
	}

	// Minimized or hidden: no frame, whatever the focus; shown again, the
	// next frame reads the engine.
	for _, focused := range []bool{true, false} {
		x.pb.SetFocused(focused)
		x.pb.SetShown(false)
		if d := x.turn(70.2); d != 0 {
			t.Errorf("away (focused %v): a frame in %v", focused, d)
		}
		x.f.Pos = 80.5
		x.pb.SetShown(true)
		if d := x.pb.Follow(); d <= 0 || x.player.Position != 80.5 {
			t.Errorf("shown again: frame in %v, at %v", d, x.player.Position)
		}
	}

	// Paused: no frame, focused or not.
	x.player.TogglePlay()
	for _, focused := range []bool{true, false} {
		x.pb.SetFocused(focused)
		if d := x.turn(90); d != 0 {
			t.Errorf("paused (focused %v): a frame in %v", focused, d)
		}
	}
}

func TestWatched(t *testing.T) {
	pb := New(nil, nil, nil)
	for _, tc := range []struct{ focused, shown, want bool }{
		{true, true, true}, {false, true, false}, {true, false, false}, {false, false, false},
	} {
		pb.SetFocused(tc.focused)
		pb.SetShown(tc.shown)
		if got := pb.IsWatched(); got != tc.want {
			t.Errorf("focused %v, shown %v: watched %v", tc.focused, tc.shown, got)
		}
	}
}

func TestPrefetchesWhileAway(t *testing.T) {
	res := &instantResolver{}
	x := newFixture(res)
	woken := make(posts, 4)
	x.pb.Wake = func(fn func()) { woken <- fn }
	x.startPlaying(t)
	next := x.player.AppendUpNext(nil, 1)[0]
	x.pb.SetShown(false)
	// 100 ms before the prefetch is due, with no frame to come.
	if d := x.turn(200 - prefetchLead - 0.1); d != 0 {
		t.Fatalf("away, a frame in %v", d)
	}
	x.f.Pos = 200 - prefetchLead + 1
	// The timer wakes the UI thread without a frame.
	select {
	case fn := <-woken:
		fn()
	case <-time.After(2 * time.Second):
		t.Fatal("the prefetch timer never woke")
	}
	x.run(t) // the resolve
	if got := res.Asked(); got[len(got)-1] != next {
		t.Fatalf("resolved %q, want %q prefetched", got, next)
	}
	if !x.pb.next.isResolved {
		t.Error("the prefetched stream was not kept")
	}
}

var errNoAudio = errors.New("no audio here")
