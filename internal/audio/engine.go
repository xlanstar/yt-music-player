package audio

import (
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	mygoaudio "github.com/egoist/mygo/audio"
)

const (
	// stallMargin: a playing stream with less decoded ahead, its source waiting
	// stallGrace for the network, buffers until resumeAhead is, checked every
	// stallPoll. The grace keeps seeks from stalling; resumeAhead, well above
	// the margin, keeps a stall from ending with every piece of a response.
	stallMargin = opusRate / 10
	stallGrace  = 200 * time.Millisecond
	resumeAhead = opusRate / 2
	stallPoll   = 100 * time.Millisecond
	// endSlack is how long after the computed end the engine looks for it,
	// so that the output has played the last frames.
	endSlack = 50 * time.Millisecond
	// endPoll is how often the end is looked for when the length is
	// unknown, or the output is late.
	endPoll = 250 * time.Millisecond
)

// output plays a decoder: MyGo's audio.Player, or a fake in tests.
type output interface {
	Play()
	Pause()
	Playing() bool
	Seek(t time.Duration)
	Position() time.Duration
	Duration() time.Duration
	setVolume(v float64)
	Close()
}

type mygoOutput struct{ *mygoaudio.Player }

func (o mygoOutput) setVolume(v float64) { o.SetVolume(v) }

func newMyGoOutput(d mygoaudio.Decoder) output { return mygoOutput{mygoaudio.NewPlayer(d)} }

// engine plays WebM Opus streams through an httpSource, an opusStream and
// MyGo's audio output. Its methods come from one thread (ytmd's loop); its
// goroutines and timers send events holding mu, which keeps them in order (a
// stall's Buffering before its Playing), never from a method.
type engine struct {
	events    func(Event)
	client    *http.Client
	newOutput func(mygoaudio.Decoder) output
	controls  controls

	// mu guards cur through isClosed, including the current load's mutable fields.
	mu     sync.Mutex
	cur    *load
	loads  uint64
	volume float64
	info   Info
	// released preserves position and duration while isReleased, until the next Load.
	released   place
	isReleased bool
	isClosed   bool
}

// place is where a stream is, in seconds, and how long it is.
type place struct{ at, seconds float64 }

// load is one Load's stream.
type load struct {
	n      uint64
	media  Media
	src    *httpSource // set once opening starts
	stream *opusStream // set with out
	out    output      // nil until ready
	// startAt is where it plays from once ready.
	startAt float64

	wantsPlay, isReady, isStalled, isDone bool
	endTimer, stallTimer                  *time.Timer
}

// New returns the engine, fetching through client (nil: http.DefaultClient),
// which needs no Timeout. events is called from other goroutines and must
// not call the engine: post them to the caller's loop.
func New(events func(Event), client *http.Client) (Engine, error) {
	if client == nil {
		client = http.DefaultClient
	}
	e := newEngine(events, client, newMyGoOutput)
	e.controls = newControls(e.command)
	return e, nil
}

func newEngine(events func(Event), client *http.Client, newOutput func(mygoaudio.Decoder) output) *engine {
	return &engine{events: events, client: client, newOutput: newOutput, controls: noControls{}, volume: 1}
}

func (e *engine) Load(media Media, at float64) {
	e.mu.Lock()
	if e.isClosed {
		e.mu.Unlock()
		return
	}
	e.dropLocked()
	e.isReleased = false
	e.loads++
	l := &load{n: e.loads, media: media, startAt: max(at, 0)}
	e.cur = l
	e.mu.Unlock()
	e.controls.claim()
	e.publish()
	go e.open(l)
}

// open fetches the stream's header and makes its output.
func (e *engine) open(l *load) {
	u, err := url.Parse(l.media.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		e.fail(l.n, errors.New("audio: bad stream URL"))
		return
	}
	// What the decoder reaches names l by number: MyGo never frees a player
	// its decoder reaches, and l holds the player.
	n := l.n
	src := newHTTPSource(e.client, l.media.URL, l.media.Header, func(waiting bool) { e.sourceWaits(n, waiting) })
	e.mu.Lock()
	if e.cur != l {
		e.mu.Unlock()
		_ = src.Close()
		return
	}
	l.src = src
	e.mu.Unlock()

	stream, err := newOpusStream(src, func(err error) { e.fail(n, err) })
	if err != nil {
		_ = src.Close()
		e.fail(n, err)
		return
	}
	out := e.newOutput(stream)
	e.mu.Lock()
	if e.cur != l {
		e.mu.Unlock()
		out.Close()
		_ = stream.Close()
		return
	}
	l.stream, l.out, l.isReady = stream, out, true
	out.setVolume(e.volume)
	if l.startAt > 0 {
		out.Seek(seconds(l.startAt))
	}
	if l.wantsPlay {
		e.startLocked(l)
	}
	e.emitLocked(l, Event{Kind: Ready})
	e.mu.Unlock()
	e.publish()
}

func seconds(sec float64) time.Duration { return time.Duration(sec * float64(time.Second)) }

// dropLocked stops the current stream; the caller holds mu.
func (e *engine) dropLocked() {
	l := e.cur
	if l == nil {
		return
	}
	e.cur = nil
	l.isDone = true
	stopTimer(l.endTimer)
	stopTimer(l.stallTimer)
	if l.out != nil {
		l.out.Close()
	}
	// The output closes the stream only once its decoding goroutine sees
	// it closed, which a read waiting on the network delays: close the
	// source now.
	if l.src != nil {
		_ = l.src.Close()
	}
}

func stopTimer(t *time.Timer) {
	if t != nil {
		t.Stop()
	}
}

func (e *engine) Play() {
	e.mu.Lock()
	if l := e.cur; l != nil && !l.wantsPlay {
		l.wantsPlay = true
		if l.isReady {
			e.startLocked(l)
		}
	}
	e.mu.Unlock()
	e.publish()
}

// startLocked plays a ready stream; the caller holds mu.
func (e *engine) startLocked(l *load) {
	l.out.Play()
	e.armEndLocked(l)
	switch {
	case l.isStalled:
		e.armStallLocked(l, 0)
	case l.src.isWaiting():
		e.watchStallLocked(l)
	}
}

func (e *engine) Pause() {
	e.mu.Lock()
	if l := e.cur; l != nil {
		l.wantsPlay = false
		if l.out != nil {
			l.out.Pause()
		}
		stopTimer(l.endTimer)
		stopTimer(l.stallTimer)
	}
	e.mu.Unlock()
	e.publish()
}

func (e *engine) Seek(sec float64) {
	sec = max(sec, 0)
	e.mu.Lock()
	if e.isReleased {
		e.released.at = sec
	}
	if l := e.cur; l != nil {
		if !l.isReady {
			l.startAt = sec
		} else {
			l.out.Seek(seconds(sec))
			if l.wantsPlay {
				e.armEndLocked(l)
			}
		}
	}
	e.mu.Unlock()
	e.publish()
}

func (e *engine) SetVolume(v float64) {
	e.mu.Lock()
	e.volume = min(max(v, 0), 1)
	if l := e.cur; l != nil && l.out != nil {
		l.out.setVolume(e.volume)
	}
	e.mu.Unlock()
}

func (e *engine) Release() {
	e.mu.Lock()
	l := e.cur
	if l == nil || l.wantsPlay {
		e.mu.Unlock()
		return
	}
	e.released, e.isReleased = place{at: e.positionLocked(), seconds: e.durationLocked()}, true
	e.dropLocked()
	e.mu.Unlock()
	e.publish()
}

func (e *engine) SetInfo(info Info) {
	e.mu.Lock()
	e.info = info
	e.mu.Unlock()
	e.publish()
}

func (e *engine) Position() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.positionLocked()
}

func (e *engine) positionLocked() float64 {
	switch l := e.cur; {
	case e.isReleased:
		return e.released.at
	case l == nil:
		return 0
	case !l.isReady:
		return l.startAt
	default:
		return l.out.Position().Seconds()
	}
}

func (e *engine) Duration() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.durationLocked()
}

// durationLocked is the file's length, else the resolver's; 0 until
// ready.
func (e *engine) durationLocked() float64 {
	if e.isReleased {
		return e.released.seconds
	}
	l := e.cur
	if l == nil || !l.isReady {
		return 0
	}
	if d := l.out.Duration(); d > 0 {
		return d.Seconds()
	}
	return l.media.Seconds
}

func (e *engine) Close() {
	e.mu.Lock()
	if e.isClosed {
		e.mu.Unlock()
		return
	}
	e.isClosed = true
	e.dropLocked()
	e.isReleased = false
	e.mu.Unlock()
	e.publish()
	e.controls.close()
}

// armEndLocked sets the timer that finds the end of a playing stream: MyGo
// tells no one when a player ends. The caller holds mu.
func (e *engine) armEndLocked(l *load) {
	wait := endPoll
	if d := l.out.Duration(); d > 0 {
		wait = max(d-l.out.Position(), 0) + endSlack
	}
	if l.endTimer == nil {
		l.endTimer = time.AfterFunc(wait, func() { e.checkEnd(l) })
		return
	}
	l.endTimer.Reset(wait)
}

func (e *engine) checkEnd(l *load) {
	e.mu.Lock()
	if l.isDone || !l.wantsPlay {
		e.mu.Unlock()
		return
	}
	if l.out.Playing() {
		// Late, as after a stall, or the file is longer than it said.
		wait := endPoll
		if d := l.out.Duration(); d > 0 {
			wait = max(d-l.out.Position()+endSlack, endPoll)
		}
		l.endTimer.Reset(wait)
		e.mu.Unlock()
		return
	}
	// The end pauses, as the system players do.
	l.wantsPlay = false
	stopTimer(l.stallTimer)
	e.emitLocked(l, Event{Kind: Ended})
	e.mu.Unlock()
	e.publish()
}

// sourceWaits hears from load n's source, on the decoding goroutine, as it
// starts waiting for the network; the stall timer looks at the rest.
func (e *engine) sourceWaits(n uint64, waiting bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if l := e.liveLocked(n); waiting && l != nil && l.isReady && l.wantsPlay && !l.isStalled {
		e.watchStallLocked(l)
	}
}

// watchStallLocked has the stall timer look again once the output may
// have played what was decoded before the source began waiting; the
// caller holds mu.
func (e *engine) watchStallLocked(l *load) {
	wait := time.Duration(float64(e.aheadLocked(l)-stallMargin) / opusRate * float64(time.Second))
	e.armStallLocked(l, max(wait, stallGrace))
}

func (e *engine) armStallLocked(l *load, wait time.Duration) {
	if l.stallTimer == nil {
		l.stallTimer = time.AfterFunc(wait, func() { e.checkStall(l) })
		return
	}
	l.stallTimer.Reset(wait)
}

// aheadLocked is how many frames were decoded past what the output played.
func (e *engine) aheadLocked(l *load) int64 {
	return l.stream.decoded.Load() - int64(l.out.Position().Seconds()*opusRate)
}

// checkStall runs on the stall timer: it reports a stall that began, or
// one that ended.
func (e *engine) checkStall(l *load) {
	e.mu.Lock()
	if l.isDone || !l.wantsPlay {
		e.mu.Unlock()
		return
	}
	ahead := e.aheadLocked(l)
	switch {
	case l.isStalled && (ahead >= resumeAhead || l.stream.atEnd.Load()):
		l.isStalled = false
		e.armEndLocked(l)
		e.emitLocked(l, Event{Kind: Playing})
	case l.isStalled:
		e.armStallLocked(l, stallPoll)
		e.mu.Unlock()
		return
	case !l.src.isWaiting():
		e.mu.Unlock()
		return
	case ahead > stallMargin:
		// The output was slower than the clock.
		e.watchStallLocked(l)
		e.mu.Unlock()
		return
	default:
		l.isStalled = true
		e.armStallLocked(l, stallPoll)
		e.emitLocked(l, Event{Kind: Buffering})
	}
	e.mu.Unlock()
	e.publish()
}

// fail reports that load n's stream cannot go on, once.
func (e *engine) fail(n uint64, err error) {
	e.mu.Lock()
	l := e.liveLocked(n)
	if l == nil {
		e.mu.Unlock()
		return
	}
	l.isDone, l.wantsPlay = true, false
	stopTimer(l.endTimer)
	stopTimer(l.stallTimer)
	e.emitLocked(l, Event{Kind: Failed, Err: err})
	e.mu.Unlock()
	e.publish()
}

// liveLocked returns load n if it is current and not done, else nil; the
// caller holds mu.
func (e *engine) liveLocked(n uint64) *load {
	if l := e.cur; l != nil && l.n == n && !l.isDone {
		return l
	}
	return nil
}

// emitLocked sends ev about l, unless the engine moved on; the caller
// holds mu.
func (e *engine) emitLocked(l *load, ev Event) {
	if e.cur == l && !e.isClosed {
		ev.Load = l.n
		e.events(ev)
	}
}

// command passes on a system command.
func (e *engine) command(ev Event) {
	e.mu.Lock()
	isClosed := e.isClosed
	e.mu.Unlock()
	if !isClosed {
		e.events(ev)
	}
}

// publish tells the system's media controls what plays.
func (e *engine) publish() {
	e.mu.Lock()
	np := nowPlaying{Info: e.info, state: stateStopped}
	if l := e.cur; (l != nil || e.isReleased) && !e.isClosed {
		np.duration = e.durationLocked()
		if np.duration == 0 {
			np.duration = e.info.Seconds
		}
		np.elapsed = e.positionLocked()
		np.state = statePaused
		if l != nil && l.wantsPlay {
			np.state = statePlaying
			np.advancing = l.isReady && !l.isStalled
		}
	}
	e.mu.Unlock()
	e.controls.publish(np)
}
