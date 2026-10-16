//go:build !darwin && !linux

package audio

// newControls has no media controls to give here yet; Windows' would be
// SystemMediaTransportControls.
func newControls(func(Event)) controls { return noControls{} }
