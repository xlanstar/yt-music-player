package audio

// controls are the system's media controls: what Now Playing shows, and
// the commands of media keys and the like, which they send to the engine.
// Their methods come from any goroutine, never holding the engine's lock.
type controls interface {
	// claim takes the media keys; Load calls it, so the app takes them
	// only once it has something to play.
	claim()
	publish(np nowPlaying)
	// close lets the media keys go, after a stopped np was published.
	close()
}

type playState uint8

const (
	stateStopped playState = iota
	statePaused
	statePlaying
)

// nowPlaying is what the controls show.
type nowPlaying struct {
	Info
	// duration and elapsed are in seconds.
	duration, elapsed float64
	state             playState
	// advancing: elapsed moves with the clock, which the system
	// extrapolates rather than being told every second.
	advancing bool
}

// noControls is for systems without media controls.
type noControls struct{}

func (noControls) claim()             {}
func (noControls) publish(nowPlaying) {}
func (noControls) close()             {}
