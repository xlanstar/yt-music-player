package audio

import "strconv"

// Fake is an Engine for tests: it plays nothing, records calls, and
// reports what the test makes it report. Not safe for concurrent use.
type Fake struct {
	// Calls records engine calls with their arguments.
	Calls []string
	// Pos and Dur are seconds; Load and Seek update Pos.
	Pos, Dur float64
	Volume   float64
	Info     Info
	Media    Media
	// Loads counts Load calls; events carry it.
	Loads uint64

	events  func(Event)
	Playing bool
	Closed  bool
}

// NewFake returns a Fake that reports to events.
func NewFake(events func(Event)) *Fake { return &Fake{events: events, Volume: 1} }

func (f *Fake) note(s string) { f.Calls = append(f.Calls, s) }

func num(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// Load records the load and moves Pos to at.
func (f *Fake) Load(media Media, at float64) {
	f.Loads++
	f.Media, f.Pos, f.Dur, f.Playing = media, at, 0, false
	f.note("load " + media.URL + " at " + num(at))
}

// Play records a play.
func (f *Fake) Play() { f.Playing = true; f.note("play") }

// Pause records a pause.
func (f *Fake) Pause() { f.Playing = false; f.note("pause") }

// Seek records the seek and moves Pos to sec.
func (f *Fake) Seek(sec float64) { f.Pos = sec; f.note("seek " + num(sec)) }

// SetVolume records the volume.
func (f *Fake) SetVolume(v float64) { f.Volume = v; f.note("volume " + num(v)) }

// Release records the release.
func (f *Fake) Release() { f.note("release") }

// SetInfo sets Info, without recording a call.
func (f *Fake) SetInfo(info Info) { f.Info = info }

// Position returns Pos.
func (f *Fake) Position() float64 { return f.Pos }

// Duration returns Dur.
func (f *Fake) Duration() float64 { return f.Dur }

// Close records the close.
func (f *Fake) Close() { f.Closed = true; f.note("close") }

// Emit reports k about the last Load.
func (f *Fake) Emit(k Kind) { f.events(Event{Kind: k, Load: f.Loads}) }

// Fail reports the last Load failed with err.
func (f *Fake) Fail(err error) { f.events(Event{Kind: StreamFailed, Load: f.Loads, Err: err}) }

// Command reports a system command.
func (f *Fake) Command(k Kind, at float64) { f.events(Event{Kind: k, At: at}) }

// TakeCalls returns the calls made since the last TakeCalls.
func (f *Fake) TakeCalls() []string {
	calls := f.Calls
	f.Calls = nil
	return calls
}
