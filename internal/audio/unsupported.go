//go:build !darwin

package audio

// New returns the system's engine; there is none here yet.
func New(func(Event)) (Engine, error) { return nil, ErrUnsupported }
