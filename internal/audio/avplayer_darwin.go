package audio

import (
	"errors"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego/objc"
)

// AVPlayerItemStatus and AVPlayerTimeControlStatus values.
const (
	itemReady  = 1
	itemFailed = 2

	timeControlWaiting = 1
	timeControlPlaying = 2
)

// avEngine plays through AVFoundation's AVPlayer, which does its own HTTP.
// Its fields are touched only on the main thread.
type avEngine struct {
	events   func(Event)
	observer objc.ID // owned; receives KVO, notifications and commands
	player   objc.ID // owned; made by the first Load
	item     objc.ID // owned
	loads    uint64  // number of the last Load

	isReady bool // the item can play and seek
	// wantsPlay: Play was asked for; it starts at startAt once ready.
	wantsPlay bool
	startAt   float64
	isStalled bool
	// Position reports seekTo until the seek completes; seekSeq ignores
	// completions of superseded seeks.
	isSeeking bool
	seekTo    float64
	seekSeq   uint64
	// realEnd, when not 0, is where the audio really ends (see setRealEnd).
	mediaSeconds, realEnd float64
	volume                float32
	info                  Info
	isClosed              bool
}

// engines maps observers to engines for the callbacks, which may come on
// any thread.
var engines sync.Map // objc.ID → *avEngine

// New returns the system's engine. events may be called from any thread:
// route them to the UI thread.
func New(events func(Event)) (Engine, error) {
	if err := loadAV(); err != nil {
		return nil, err
	}
	if err := registerObserver(); err != nil {
		return nil, err
	}
	e := &avEngine{events: events, volume: 1}
	onMain(func() { e.observer = send(send(class("YTMAudioObserver"), "alloc"), "init") })
	engines.Store(e.observer, e)
	return e, nil
}

// registerObserver registers the observer class once: each method is a
// purego callback, which is never freed.
var registerObserver = sync.OnceValue(func() error {
	withEngine := func(fn func(e *avEngine, arg objc.ID)) func(objc.ID, objc.SEL, objc.ID) {
		return func(self objc.ID, _ objc.SEL, arg objc.ID) {
			if v, ok := engines.Load(self); ok {
				fn(v.(*avEngine), arg)
			}
		}
	}
	commandHandler := func(k Kind) func(objc.ID, objc.SEL, objc.ID) int {
		return func(self objc.ID, _ objc.SEL, event objc.ID) int {
			at := 0.0
			if k == CommandSeek {
				at = msgF64(event, sel("positionTime"))
			}
			if v, ok := engines.Load(self); ok {
				e := v.(*avEngine)
				later(func() { e.emit(Event{Kind: k, At: at}) })
			}
			return 0 // MPRemoteCommandHandlerStatusSuccess
		}
	}
	methods := []objc.MethodDef{
		{
			Cmd: sel("observeValueForKeyPath:ofObject:change:context:"),
			Fn: func(self objc.ID, _ objc.SEL, _, obj, _ objc.ID, _ unsafe.Pointer) {
				if v, ok := engines.Load(self); ok {
					e := v.(*avEngine)
					later(func() { e.statusChanged(obj) })
				}
			},
		},
		{Cmd: sel("itemEnded:"), Fn: withEngine(func(e *avEngine, note objc.ID) {
			item := send(note, "object")
			later(func() { e.ended(item) })
		})},
		{Cmd: sel("itemFailed:"), Fn: withEngine(func(e *avEngine, note objc.ID) {
			item := send(note, "object")
			info := send(note, "userInfo")
			err := errors.New("audio: the stream stopped")
			if key := constant(libAV, "AVPlayerItemFailedToPlayToEndTimeErrorKey"); key != 0 && info != 0 {
				err = nsError(send(info, "objectForKey:", key))
			}
			later(func() { e.failed(item, err) })
		})},
	}
	for _, c := range remoteCommands {
		methods = append(methods, objc.MethodDef{Cmd: sel(c.action), Fn: commandHandler(c.kind)})
	}
	_, err := objc.RegisterClass("YTMAudioObserver", objc.GetClass("NSObject"), nil, nil, methods)
	return err
})

func (e *avEngine) emit(ev Event) {
	if !e.isClosed {
		e.events(ev)
	}
}

func (e *avEngine) Load(media Media, at float64) {
	onMain(func() {
		e.dropItem()
		e.loads++
		e.isReady, e.wantsPlay, e.isStalled, e.isSeeking, e.startAt = false, false, false, false, at
		e.mediaSeconds, e.realEnd = media.Seconds, 0
		if e.player == 0 {
			e.player = send(send(class("AVPlayer"), "alloc"), "init")
			send(e.player, "addObserver:forKeyPath:options:context:",
				e.observer, nsString("timeControlStatus"), uint(0), uintptr(0))
			msgSetF32(e.player, sel("setVolume:"), e.volume)
		}
		send(e.player, "pause")
		asset, err := urlAsset(media)
		if err != nil {
			load := e.loads
			send(e.player, "replaceCurrentItemWithPlayerItem:", objc.ID(0))
			later(func() { e.emit(Event{Kind: Failed, Load: load, Err: err}) })
			return
		}
		e.item = send(send(class("AVPlayerItem"), "alloc"), "initWithAsset:", asset)
		send(e.item, "addObserver:forKeyPath:options:context:", e.observer, nsString("status"), uint(0), uintptr(0))
		center := send(class("NSNotificationCenter"), "defaultCenter")
		for name, action := range map[string]string{
			"AVPlayerItemDidPlayToEndTimeNotification":      "itemEnded:",
			"AVPlayerItemFailedToPlayToEndTimeNotification": "itemFailed:",
		} {
			if notification := constant(libAV, name); notification != 0 {
				send(center, "addObserver:selector:name:object:", e.observer, sel(action), notification, e.item)
			}
		}
		send(e.player, "replaceCurrentItemWithPlayerItem:", e.item)
		e.claimRemoteCommands()
		e.publish()
	})
}

// urlAsset returns an autoreleased AVURLAsset that sends media's headers.
func urlAsset(media Media) (objc.ID, error) {
	url := send(class("NSURL"), "URLWithString:", nsString(media.URL))
	if url == 0 {
		return 0, errors.New("audio: bad stream URL")
	}
	opts := send(class("NSMutableDictionary"), "dictionary")
	if len(media.Header) > 0 {
		headers := send(class("NSMutableDictionary"), "dictionary")
		for k, v := range media.Header {
			send(headers, "setObject:forKey:", nsString(strings.Join(v, ", ")), nsString(k))
		}
		// Private, but it is where AVFoundation reads headers from.
		send(opts, "setObject:forKey:", headers, nsString("AVURLAssetHTTPHeaderFieldsKey"))
		// Public since macOS 13; the headers key may not set the User-Agent.
		if ua := media.Header.Get("User-Agent"); ua != "" {
			if key := constant(libAV, "AVURLAssetHTTPUserAgentKey"); key != 0 {
				send(opts, "setObject:forKey:", nsString(ua), key)
			}
		}
	}
	return send(class("AVURLAsset"), "URLAssetWithURL:options:", url, opts), nil
}

// dropItem removes the item's observers before releasing it: KVO raises
// an exception for an object freed while observed.
func (e *avEngine) dropItem() {
	if e.item == 0 {
		return
	}
	send(e.item, "removeObserver:forKeyPath:", e.observer, nsString("status"))
	center := send(class("NSNotificationCenter"), "defaultCenter")
	send(center, "removeObserver:name:object:", e.observer, objc.ID(0), e.item)
	send(e.item, "release")
	e.item = 0
}

func (e *avEngine) Play() {
	onMain(func() {
		e.wantsPlay = true
		if e.isReady {
			send(e.player, "play")
		}
		e.publish()
	})
}

func (e *avEngine) Pause() {
	onMain(func() {
		e.wantsPlay = false
		if e.player != 0 {
			send(e.player, "pause")
		}
		e.publish()
	})
}

func (e *avEngine) Seek(sec float64) {
	onMain(func() {
		if !e.isReady {
			e.startAt = sec
			return
		}
		e.seek(sec)
	})
}

// seek needs a ready item: a seek with a completion handler raises an
// exception before then, so a Seek before ready waits in startAt.
func (e *avEngine) seek(sec float64) {
	e.seekSeq++
	seq := e.seekSeq
	e.isSeeking, e.seekTo = true, sec
	done := objc.NewBlock(func(_ objc.Block, _ bool) {
		later(func() {
			if seq == e.seekSeq {
				e.isSeeking = false
				e.publish()
			}
		})
	})
	zero := cmTime{Timescale: 1, Flags: timeValid}
	msgSeek(e.player, sel("seekToTime:toleranceBefore:toleranceAfter:completionHandler:"), timeAt(sec), zero, zero, done)
	done.Release()
	e.publish()
}

func (e *avEngine) SetVolume(v float64) {
	onMain(func() {
		e.volume = float32(min(max(v, 0), 1))
		if e.player != 0 {
			msgSetF32(e.player, sel("setVolume:"), e.volume)
		}
	})
}

func (e *avEngine) SetInfo(info Info) {
	onMain(func() {
		e.info = info
		e.publish()
	})
}

func (e *avEngine) Position() float64 {
	// Read at every step of the shown position, on the main thread: skip
	// onMain's pool, as reading makes no object.
	if isMain() {
		return e.position()
	}
	var pos float64
	onMain(func() { pos = e.position() })
	return pos
}

func (e *avEngine) position() float64 {
	switch {
	case !e.isReady:
		return e.startAt
	case e.isSeeking:
		return e.seekTo
	}
	return seconds(msgTime(e.player, sel("currentTime")))
}

func (e *avEngine) Duration() float64 {
	var d float64
	onMain(func() { d = e.duration() })
	return d
}

func (e *avEngine) duration() float64 {
	switch {
	case !e.isReady:
		return 0
	case e.realEnd > 0:
		return e.realEnd
	}
	return seconds(msgTime(e.item, sel("duration")))
}

// setRealEnd ends the item where its audio does (see halvedEnd), by the
// Media's length, else the catalog's.
func (e *avEngine) setRealEnd() {
	length := e.mediaSeconds
	if length <= 0 {
		length = e.info.Seconds
	}
	if e.realEnd = halvedEnd(seconds(msgTime(e.item, sel("duration"))), length); e.realEnd > 0 {
		msgSetTime(e.item, sel("setForwardPlaybackEndTime:"), timeAt(e.realEnd))
	}
}

func (e *avEngine) Close() {
	onMain(func() {
		if e.isClosed {
			return
		}
		e.isClosed = true
		e.dropItem()
		if e.player != 0 {
			send(e.player, "removeObserver:forKeyPath:", e.observer, nsString("timeControlStatus"))
			send(e.player, "pause")
			send(e.player, "replaceCurrentItemWithPlayerItem:", objc.ID(0))
			send(e.player, "release")
			e.player = 0
		}
		e.releaseRemoteCommands()
		e.publish()
		engines.Delete(e.observer)
		send(e.observer, "release")
	})
}

// statusChanged handles KVO of the item's status and the player's
// timeControlStatus.
func (e *avEngine) statusChanged(obj objc.ID) {
	switch {
	case obj == 0 || e.isClosed:
	case obj == e.item:
		switch int(send(e.item, "status")) {
		case itemReady:
			if e.isReady {
				return
			}
			e.isReady = true
			e.setRealEnd()
			if e.startAt > 0 {
				e.seek(e.startAt)
			}
			if e.wantsPlay {
				send(e.player, "play")
			}
			e.publish()
			e.emit(Event{Kind: Ready, Load: e.loads})
		case itemFailed:
			e.emit(Event{Kind: Failed, Load: e.loads, Err: nsError(send(e.item, "error"))})
		}
	case obj == e.player && e.isReady:
		switch int(send(e.player, "timeControlStatus")) {
		case timeControlWaiting:
			if e.wantsPlay && !e.isStalled {
				e.isStalled = true
				e.publish()
				e.emit(Event{Kind: Buffering, Load: e.loads})
			}
		case timeControlPlaying:
			if e.isStalled {
				e.isStalled = false
				e.publish()
				e.emit(Event{Kind: Playing, Load: e.loads})
			}
		}
	}
}

func (e *avEngine) ended(item objc.ID) {
	if item != e.item || item == 0 {
		return
	}
	// AVPlayer pauses itself at the end.
	e.wantsPlay = false
	e.publish()
	e.emit(Event{Kind: Ended, Load: e.loads})
}

func (e *avEngine) failed(item objc.ID, err error) {
	if item == e.item && item != 0 {
		e.emit(Event{Kind: Failed, Load: e.loads, Err: err})
	}
}
