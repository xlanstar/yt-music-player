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
	statePlaying = 1
	statePaused  = 2
	stateStopped = 3
)

type nowPlayingKeys struct {
	title, artist, album, duration, elapsed, rate objc.ID
}

// loadNowPlayingKeys loads MediaPlayer's nowPlayingInfo keys; nil without
// MediaPlayer.
var loadNowPlayingKeys = sync.OnceValue(func() *nowPlayingKeys {
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
		keys.duration == 0 || keys.elapsed == 0 || keys.rate == 0 {
		return nil
	}
	return keys
})

// claimRemoteCommands takes the media keys; Load calls it, so the app
// takes them only once it has something to play. Removing the target
// first keeps each Load from adding it again.
func (e *avEngine) claimRemoteCommands() {
	if loadNowPlayingKeys() == nil {
		return
	}
	center := send(class("MPRemoteCommandCenter"), "sharedCommandCenter")
	for _, c := range remoteCommands {
		cmd := send(center, c.getter)
		send(cmd, "removeTarget:", e.observer)
		send(cmd, "addTarget:action:", e.observer, sel(c.action))
		send(cmd, "setEnabled:", true)
	}
}

func (e *avEngine) releaseRemoteCommands() {
	if loadNowPlayingKeys() == nil {
		return
	}
	center := send(class("MPRemoteCommandCenter"), "sharedCommandCenter")
	for _, c := range remoteCommands {
		send(send(center, c.getter), "removeTarget:", e.observer)
	}
}

func (e *avEngine) publish() {
	keys := loadNowPlayingKeys()
	if keys == nil {
		return
	}
	center := send(class("MPNowPlayingInfoCenter"), "defaultCenter")
	if e.isClosed || e.item == 0 {
		send(center, "setNowPlayingInfo:", objc.ID(0))
		send(center, "setPlaybackState:", uint(stateStopped))
		return
	}
	info := send(class("NSMutableDictionary"), "dictionary")
	set := func(key, v objc.ID) { send(info, "setObject:forKey:", v, key) }
	set(keys.title, nsString(e.info.Title))
	set(keys.artist, nsString(e.info.Artist))
	if e.info.Album != "" {
		set(keys.album, nsString(e.info.Album))
	}
	duration := e.duration()
	if duration == 0 {
		duration = e.info.Seconds
	}
	if duration > 0 {
		set(keys.duration, nsNumber(duration))
	}
	playing := e.wantsPlay && e.isReady && !e.isStalled && !e.isSeeking
	rate := 0.0
	if playing {
		rate = 1
	}
	set(keys.elapsed, nsNumber(e.position()))
	set(keys.rate, nsNumber(rate))
	send(center, "setNowPlayingInfo:", info)
	state := statePaused
	if e.wantsPlay {
		state = statePlaying
	}
	send(center, "setPlaybackState:", uint(state))
}
