package playback

import (
	"context"
	"errors"
	"math"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
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
	x.player.Seek(84)
	var events func(audio.Event)
	x.f = audio.NewFake(func(ev audio.Event) { events(ev) })
	x.pb = New(x.cat, x.player, func(fn func()) { x.q <- fn }, Options{
		Resolver: res,
		NewEngine: func(ev func(audio.Event)) (audio.Engine, error) {
			events = ev
			return x.f, nil
		},
	})
	return x
}

// setRepeat cycles p's repeat mode to r, as a front-end does.
func setRepeat(p *player.Player, r player.Repeat) {
	for p.Repeat() != r {
		p.CycleRepeat()
	}
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
	x.player.Seek(0)
	x.player.Resume()
	x.pb.Follow()
	x.run(t)
	x.f.Dur = 200
	x.f.Emit(audio.Ready)
	x.run(t)
	x.f.TakeCalls()
}

// turn moves the engine to pos and has Follow read it, as the next
// request or event does.
func (x *fixture) turn(pos float64) {
	x.f.Pos = pos
	x.pb.Follow()
}

func TestLoadsAndPlays(t *testing.T) {
	res := &instantResolver{}
	x := newFixture(res)
	first := x.player.Current()
	x.pb.Follow()
	if len(res.Asked()) != 0 {
		t.Fatalf("resolved %q while paused", res.Asked())
	}
	x.player.Resume()
	x.pb.Follow()
	x.run(t)
	// The mock starts part-way into the song.
	x.wantCalls(t, "volume 0.7", "load https://audio/"+first+" at 84", "play")
	if x.f.Info.Title == "" || x.f.Info.Seconds == 0 {
		t.Errorf("info %+v", x.f.Info)
	}
	x.turn(90)
	if x.player.Position() != 84 {
		t.Errorf("at %v before the stream is ready, want 84", x.player.Position())
	}
	x.f.Dur, x.f.Pos = 201.5, 84.25
	x.f.Emit(audio.Ready)
	x.run(t)
	song, _ := x.cat.Song(first)
	if got := x.pb.SongLength(song); got != 201.5 {
		t.Errorf("length %v, want the engine's 201.5", got)
	}
	if x.pb.Follow(); x.player.Position() != 84.25 {
		t.Errorf("at %v once ready, want the engine's 84.25", x.player.Position())
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
	x.player.Resume()
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
	if x.player.Current() == first || !x.player.IsPlaying() {
		t.Fatalf("after the end at %q (playing %v)", x.player.Current(), x.player.IsPlaying())
	}
	// The engine already paused at the end; pausing again is harmless.
	x.wantCalls(t, "pause", "load https://audio/"+x.player.Current()+" at 0", "play")

	x.f.Emit(audio.Ready)
	x.run(t)
	setRepeat(x.player, player.RepeatOne)
	x.f.Pos = 200
	x.f.Emit(audio.Ended)
	x.run(t)
	x.wantCalls(t, "seek 0", "play")
	if x.player.Position() != 0 || !x.player.IsPlaying() {
		t.Errorf("repeat one at %v playing %v", x.player.Position(), x.player.IsPlaying())
	}
	// Events of a stream replaced since are stale.
	setRepeat(x.player, player.RepeatOff)
	x.pb.audioEvent(audio.Event{Kind: audio.Ended, Load: x.f.Loads - 1})
	x.wantCalls(t)
}

func TestEndOfQueueStops(t *testing.T) {
	x := newFixture(&instantResolver{})
	x.player.PlayAll([]string{x.cat.SongIDs()[0]}, 0)
	x.startPlaying(t)
	x.f.Emit(audio.Ended)
	x.run(t)
	if x.player.IsPlaying() {
		t.Error("still playing past the end of the queue")
	}
	x.wantCalls(t, "seek 0")

	setRepeat(x.player, player.RepeatAll)
	x.player.Resume()
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
	setRepeat(x.player, player.RepeatAll)
	x.player.Resume()
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
	if n, ok := x.pb.TakeNotice(); x.player.IsPlaying() || !ok || !reflect.DeepEqual(n, want) {
		t.Errorf("playing %v, notice %+v, want %+v", x.player.IsPlaying(), n, want)
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
	x.player.PlayAll(ids, 0)
	setRepeat(x.player, player.RepeatAll)
	x.player.Resume()
	x.pb.Follow()
	x.failAll()
	// A queue shorter than maxSkips stops once each song failed.
	if n := len(res.Asked()); n != len(ids) {
		t.Errorf("tried %d songs, want %d", n, len(ids))
	}
	if n, _ := x.pb.TakeNotice(); x.player.IsPlaying() || n.Detail != notice.PlaybackStoppedAfter || n.N != 3 {
		t.Errorf("playing %v, notice %+v", x.player.IsPlaying(), n)
	}
}

func TestEngineFails(t *testing.T) {
	x := newFixture(&instantResolver{})
	x.pb.newEngine = func(func(audio.Event)) (audio.Engine, error) { return nil, errNoAudio }
	x.player.Resume()
	x.pb.Follow()
	x.run(t)
	if n, ok := x.pb.TakeNotice(); !ok || n.Title != notice.NoAudio || !slices.Equal(n.Args, []string{errNoAudio.Error()}) {
		t.Errorf("notice %+v", n)
	}
	// The player goes on as a mock.
	x.player.Next()
	if x.pb.Follow(); len(x.q) != 0 {
		t.Errorf("without an engine: %d posts", len(x.q))
	}
}

func TestSeekAndVolumeAreForwarded(t *testing.T) {
	x := newFixture(&instantResolver{})
	x.startPlaying(t)
	x.player.Seek(42)
	x.player.SetVolume(0.3)
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

// A system's seek is checked as the player's own: Now Playing may send
// any number.
func TestSystemSeekIsChecked(t *testing.T) {
	x := newFixture(&instantResolver{})
	x.startPlaying(t)
	x.turn(42)
	x.pb.audioEvent(audio.Event{Kind: audio.CommandSeek, At: math.NaN()})
	if x.player.Position() != 42 {
		t.Errorf("NaN moved to %v, want 42", x.player.Position())
	}
	x.wantCalls(t)
	x.pb.audioEvent(audio.Event{Kind: audio.CommandSeek, At: -5})
	if x.player.Position() != 0 {
		t.Errorf("-5 moved to %v, want 0", x.player.Position())
	}
	x.wantCalls(t, "seek 0")
	x.player.Clear()
	x.pb.Follow()
	x.f.TakeCalls()
	x.pb.audioEvent(audio.Event{Kind: audio.CommandSeek, At: 30})
	if x.player.Position() != 0 {
		t.Errorf("an empty queue moved to %v, want 0", x.player.Position())
	}
	x.wantCalls(t)
}

// Paused or stalled, the position stands still, whatever the engine says.
func TestPositionStandsStill(t *testing.T) {
	x := newFixture(&instantResolver{})
	x.startPlaying(t)
	if x.turn(10.5); x.player.Position() != 10.5 {
		t.Fatalf("playing at %v, want 10.5", x.player.Position())
	}
	x.player.TogglePlay()
	x.pb.Follow()
	x.wantCalls(t, "pause")
	if x.turn(30); x.player.Position() != 10.5 {
		t.Errorf("paused at %v, want 10.5", x.player.Position())
	}
	x.noPost(t, 1200*time.Millisecond)

	x.player.TogglePlay()
	x.pb.Follow()
	x.f.Emit(audio.Buffering)
	x.run(t)
	if x.turn(40); x.player.Position() != 30 {
		t.Errorf("buffering at %v, want 30", x.player.Position())
	}
	x.f.Emit(audio.Playing)
	x.run(t)
	if x.turn(41); x.player.Position() != 41 {
		t.Errorf("playing on at %v, want 41", x.player.Position())
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
	if !x.pb.prefetch.next.isResolved {
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
	if x.player.IsPlaying() {
		t.Error("toggle did not pause")
	}
	x.f.Command(audio.CommandPlay, 0)
	x.run(t)
	if !x.player.IsPlaying() {
		t.Error("play did not play")
	}
	x.wantCalls(t, "pause", "play")
	// Prev 30 s into the song restarts it, reading where the engine is.
	x.f.Pos = 30
	x.pb.audioEvent(audio.Event{Kind: audio.CommandPrev})
	if x.player.Current() != first || x.player.Position() != 0 {
		t.Errorf("prev 30 s in went to %q at %v", x.player.Current(), x.player.Position())
	}
	x.pb.audioEvent(audio.Event{Kind: audio.CommandNext})
	x.run(t)
	if x.player.Current() == first {
		t.Error("next stayed")
	}
}

func TestNoPlaybackWithoutResolver(t *testing.T) {
	x := newFixture(nil)
	x.pb.newEngine = func(func(audio.Event)) (audio.Engine, error) {
		t.Error("made an engine with no resolver")
		return nil, errNoAudio
	}
	x.player.Resume()
	if x.pb.Follow(); len(x.q) != 0 || x.pb.state != unloaded {
		t.Errorf("no resolver: %d posts, state %v", len(x.q), x.pb.state)
	}
}

func TestStop(t *testing.T) {
	x := newFixture(&instantResolver{})
	x.startPlaying(t)
	x.pb.Stop()
	x.wantCalls(t, "close")
	x.player.Next()
	if x.pb.Follow(); len(x.q) != 0 {
		t.Errorf("stopped: %d posts", len(x.q))
	}
}

// The prefetch comes on a timer: playing, nothing else wakes the loop.
func TestPrefetchesOnItsTimer(t *testing.T) {
	res := &instantResolver{}
	x := newFixture(res)
	x.startPlaying(t)
	next := x.player.AppendUpNext(nil, 1)[0]
	// 100 ms before the prefetch is due.
	x.turn(200 - prefetchLead - 0.1)
	x.f.Pos = 200 - prefetchLead + 1
	if len(x.q) != 0 {
		t.Fatalf("%d posts before the timer's", len(x.q))
	}
	select {
	case fn := <-x.q:
		fn()
	case <-time.After(2 * time.Second):
		t.Fatal("the prefetch timer never woke")
	}
	x.run(t) // the resolve
	if got := res.Asked(); got[len(got)-1] != next {
		t.Fatalf("resolved %q, want %q prefetched", got, next)
	}
	if !x.pb.prefetch.next.isResolved {
		t.Error("the prefetched stream was not kept")
	}
}

var errNoAudio = errors.New("no audio here")

func TestWaiting(t *testing.T) {
	x := newFixture(&instantResolver{})
	if x.pb.IsWaiting() {
		t.Error("waiting while paused")
	}
	x.player.Resume()
	x.pb.Follow()
	if !x.pb.IsWaiting() {
		t.Error("not waiting while resolving")
	}
	x.run(t)
	if !x.pb.IsWaiting() {
		t.Error("not waiting while loading")
	}
	x.f.Dur = 200
	x.f.Emit(audio.Ready)
	x.run(t)
	if x.pb.IsWaiting() {
		t.Error("waiting once ready")
	}
	x.f.Emit(audio.Buffering)
	x.run(t)
	if !x.pb.IsWaiting() {
		t.Error("not waiting while stalled")
	}
	x.f.Emit(audio.Playing)
	x.run(t)
	if x.pb.IsWaiting() {
		t.Error("waiting after the stall")
	}

	silent := newFixture(nil)
	silent.player.Resume()
	if !silent.pb.IsWaiting() {
		t.Error("playing without a Resolver moves on")
	}
}

// released advances the bubble's clock to the release and runs its post.
func (x *fixture) released(t *testing.T) {
	t.Helper()
	synctest.Sleep(releaseAfter)
	x.posted(t)
}

// posted runs the post made once the bubble's goroutines are blocked.
func (x *fixture) posted(t *testing.T) {
	t.Helper()
	synctest.Wait()
	select {
	case fn := <-x.q:
		fn()
	default:
		t.Fatal("nothing posted")
	}
}

// none fails if anything was posted once the bubble's goroutines are
// blocked.
func (x *fixture) none(t *testing.T) {
	t.Helper()
	synctest.Wait()
	select {
	case <-x.q:
		t.Fatal("posted, want nothing")
	default:
	}
}

func TestReleasesAPausedStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		res := &instantResolver{}
		x := newFixture(res)
		song := x.player.Current()
		x.startPlaying(t)
		x.turn(50)
		x.player.Pause()
		x.pb.Follow()
		x.released(t)
		x.pb.Follow()
		x.wantCalls(t, "pause", "release")
		if x.pb.IsWaiting() || x.player.Position() != 50 {
			t.Errorf("released: waiting %v, at %v", x.pb.IsWaiting(), x.player.Position())
		}
		// Seeks move Now Playing.
		x.player.Seek(60)
		x.pb.Follow()
		x.wantCalls(t, "seek 60")

		// Play loads the same stream again, where the song was.
		x.player.Resume()
		x.pb.Follow()
		x.wantCalls(t, "load https://audio/"+song+" at 60", "play")
		if got := res.Asked(); len(got) != 1 {
			t.Errorf("resolved %q, want the released stream", got)
		}
		if !x.pb.IsWaiting() {
			t.Error("not waiting while it loads again")
		}
		x.f.Emit(audio.Ready)
		x.posted(t)
		if x.pb.IsWaiting() {
			t.Error("waiting once ready")
		}
		x.none(t)
	})
}

func TestReleaseWaitsForThePause(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		x := newFixture(&instantResolver{})
		x.startPlaying(t)
		synctest.Sleep(releaseAfter + time.Second)
		x.none(t)
		// Played again a second before it is due.
		x.player.Pause()
		x.pb.Follow()
		synctest.Sleep(releaseAfter - time.Second)
		x.player.Resume()
		x.pb.Follow()
		synctest.Sleep(2 * time.Second)
		x.none(t)
		x.wantCalls(t, "pause", "play")
	})
}

func TestReleasedStreamThatExpiresResolvesAnew(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		res := &instantResolver{expires: time.Now().Add(expirySlack / 2)}
		x := newFixture(res)
		song := x.player.Current()
		x.startPlaying(t)
		x.player.Pause()
		x.pb.Follow()
		x.released(t)
		x.player.Resume()
		x.pb.Follow()
		x.posted(t) // the resolve
		x.wantCalls(t, "pause", "release", "load https://audio/"+song+" at 0", "play")
		if got := res.Asked(); len(got) != 2 {
			t.Errorf("resolved %q, want the song again", got)
		}
	})
}
