package audio

import (
	"sync"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// Now Playing and media keys go through MediaPlayer's
// MPNowPlayingInfoCenter and MPRemoteCommandCenter. The system extrapolates
// progress from nowPlayingInfo's rate and elapsed time, so it is published
// on every change rather than as the song plays.

// remoteCommands are MPRemoteCommandCenter's getters for the commands the
// engine handles, with the observer's selector for each.
var remoteCommands = []struct {
	getter, action string
	kind           Kind
}{
	{"playCommand", "commandPlay:", CommandPlay},
	{"pauseCommand", "commandPause:", CommandPause},
	{"togglePlayPauseCommand", "commandToggle:", CommandToggle},
	{"nextTrackCommand", "commandNext:", CommandNext},
	{"previousTrackCommand", "commandPrev:", CommandPrev},
	{"changePlaybackPositionCommand", "commandSeek:", CommandSeek},
}

// MPNowPlayingPlaybackState values.
const (
	mpPlaying = 1
	mpPaused  = 2
	mpStopped = 3
)

type nowPlayingKeys struct {
	title, artist, album, duration, elapsed, rate objc.ID
}

// loadNowPlayingKeys loads MediaPlayer's nowPlayingInfo keys; nil without
// MediaPlayer.
var loadNowPlayingKeys = sync.OnceValue(func() *nowPlayingKeys {
	if loadObjC() != nil {
		return nil
	}
	const path = "/System/Library/Frameworks/MediaPlayer.framework/MediaPlayer"
	lib, err := purego.Dlopen(path, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil || objc.GetClass("MPNowPlayingInfoCenter") == 0 {
		return nil
	}
	keys := &nowPlayingKeys{
		title:    constant(lib, "MPMediaItemPropertyTitle"),
		artist:   constant(lib, "MPMediaItemPropertyArtist"),
		album:    constant(lib, "MPMediaItemPropertyAlbumTitle"),
		duration: constant(lib, "MPMediaItemPropertyPlaybackDuration"),
		elapsed:  constant(lib, "MPNowPlayingInfoPropertyElapsedPlaybackTime"),
		rate:     constant(lib, "MPNowPlayingInfoPropertyPlaybackRate"),
	}
	if keys.title == 0 || keys.artist == 0 || keys.album == 0 ||
		keys.duration == 0 || keys.elapsed == 0 || keys.rate == 0 ||
		registerObserver() != nil {
		return nil
	}
	return keys
})

// observers maps observers to their controls' command funcs, for the
// callbacks.
var observers sync.Map // objc.ID → func(Event)

// registerObserver registers the observer class once: each method is a
// purego callback, which is never freed.
var registerObserver = sync.OnceValue(func() error {
	handler := func(k Kind) func(objc.ID, objc.SEL, objc.ID) int {
		return func(self objc.ID, _ objc.SEL, event objc.ID) int {
			at := 0.0
			if k == CommandSeek {
				at = msgF64(event, sel("positionTime"))
			}
			if v, ok := observers.Load(self); ok {
				v.(func(Event))(Event{Kind: k, At: at})
			}
			return 0 // MPRemoteCommandHandlerStatusSuccess
		}
	}
	var methods []objc.MethodDef
	for _, c := range remoteCommands {
		methods = append(methods, objc.MethodDef{Cmd: sel(c.action), Fn: handler(c.kind)})
	}
	_, err := objc.RegisterClass("YTMCommandObserver", objc.GetClass("NSObject"), nil, nil, methods)
	return err
})

// macControls drive MediaPlayer on the main thread: publish asynchronously,
// in order, as the engine calls it from any goroutine.
type macControls struct {
	keys     *nowPlayingKeys
	observer objc.ID // owned
}

func newControls(command func(Event)) controls {
	keys := loadNowPlayingKeys()
	if keys == nil {
		return noControls{}
	}
	c := &macControls{keys: keys}
	onMain(func() { c.observer = send(send(class("YTMCommandObserver"), "alloc"), "init") })
	// Commands come on the main thread, from MediaPlayer's handler: pass
	// them on after it, in order.
	observers.Store(c.observer, func(ev Event) { later(func() { command(ev) }) })
	return c
}

// claim removes the target first, so that each Load adds it once.
func (c *macControls) claim() {
	later(func() {
		center := send(class("MPRemoteCommandCenter"), "sharedCommandCenter")
		for _, rc := range remoteCommands {
			cmd := send(center, rc.getter)
			send(cmd, "removeTarget:", c.observer)
			send(cmd, "addTarget:action:", c.observer, sel(rc.action))
			send(cmd, "setEnabled:", true)
		}
	})
}

func (c *macControls) publish(np nowPlaying) {
	later(func() { c.apply(np) })
}

func (c *macControls) apply(np nowPlaying) {
	center := send(class("MPNowPlayingInfoCenter"), "defaultCenter")
	if np.state == stateStopped {
		send(center, "setNowPlayingInfo:", objc.ID(0))
		send(center, "setPlaybackState:", uint(mpStopped))
		return
	}
	keys := c.keys
	info := send(class("NSMutableDictionary"), "dictionary")
	set := func(key, v objc.ID) { send(info, "setObject:forKey:", v, key) }
	set(keys.title, nsString(np.Title))
	set(keys.artist, nsString(np.Artist))
	if np.Album != "" {
		set(keys.album, nsString(np.Album))
	}
	if np.duration > 0 {
		set(keys.duration, nsNumber(np.duration))
	}
	rate := 0.0
	if np.advancing {
		rate = 1
	}
	set(keys.elapsed, nsNumber(np.elapsed))
	set(keys.rate, nsNumber(rate))
	send(center, "setNowPlayingInfo:", info)
	state := mpPaused
	if np.state == statePlaying {
		state = mpPlaying
	}
	send(center, "setPlaybackState:", uint(state))
}

func (c *macControls) close() {
	later(func() {
		center := send(class("MPRemoteCommandCenter"), "sharedCommandCenter")
		for _, rc := range remoteCommands {
			send(send(center, rc.getter), "removeTarget:", c.observer)
		}
		observers.Delete(c.observer)
		send(c.observer, "release")
	})
}
