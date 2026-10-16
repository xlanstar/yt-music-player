// Package audio plays a song's stream and tells the system what plays, so
// media keys and Now Playing control it, the same on every system: the
// stream, a WebM file of Opus, is fetched over HTTP (source.go), read
// (internal/webm) and decoded in Go (opus.go), and MyGo's audio output
// plays it (engine.go). The system's media controls are per system:
// MediaPlayer on macOS, MPRIS on Linux, none on Windows yet.
package audio

import "net/http"

// Media is what an Engine plays; it keeps audio independent of any resolver.
type Media struct {
	URL string
	// Seconds is the song's length, 0 when unknown, for a file that does
	// not say.
	Seconds float64
	// Header is sent when fetching URL (e.g. User-Agent).
	Header http.Header
}

// Engine plays one stream at a time. Call its methods from the UI thread.
// It reports through the events function given to New, never from within
// one of its own methods.
type Engine interface {
	// Load replaces the stream, starting at seconds, paused until Play.
	// Loads are numbered from 1; their events carry that number.
	Load(s Media, at float64)
	Play()
	Pause()
	Seek(sec float64)
	// SetVolume sets the gain, 0 to 1.
	SetVolume(v float64)
	// SetInfo describes what plays to Now Playing.
	SetInfo(Info)
	// Position and Duration are in seconds; Duration is 0 until known.
	Position() float64
	Duration() float64
	Close()
}

// Info describes the song playing.
type Info struct {
	Title, Artist, Album string
	// Seconds is the catalog's length, used until the stream reports one.
	Seconds float64
}

// Kind is the kind of an Event.
type Kind uint8

// Stream events, then the system's commands (media keys, Now Playing).
// Commands must stay last: IsCommand compares against CommandPlay.
const (
	// Ready means the stream loaded: Duration is known and Seek works.
	Ready Kind = iota
	// Buffering means playback waits for data; Playing that it resumed.
	Buffering
	Playing
	Ended
	// Failed means the stream cannot play; Err says why.
	Failed

	CommandPlay
	CommandPause
	CommandToggle
	CommandNext
	CommandPrev
	// CommandSeek asks to play from At.
	CommandSeek
)

// Event is something an Engine reports.
type Event struct {
	Kind Kind
	// Load numbers the Load the event is about, so events of a replaced
	// stream can be dropped; 0 for a command.
	Load uint64
	Err  error
	// At is CommandSeek's target, in seconds.
	At float64
}

// IsCommand reports whether e is a system command rather than news of the
// stream.
func (e Event) IsCommand() bool { return e.Kind >= CommandPlay }
