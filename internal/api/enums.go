package api

import (
	"errors"
	"fmt"
	"strconv"
)

// ErrInvalid is wrapped by the errors of Validate: a frame no peer of this
// Proto sends.
var ErrInvalid = errors.New("api: invalid frame")

// Method is what a Call asks for. Values travel as numbers: add new ones
// only before numMethods, never reorder or remove one.
type Method uint16

// The methods. The first three are bootstrap: their numbers never change.
const (
	_ Method = iota
	MethodPing
	MethodHandoff
	MethodShutdown
	MethodStatus
	MethodPlay
	MethodPlayAll
	MethodToggle
	MethodPause
	MethodResume
	MethodNext
	MethodPrev
	MethodSeek
	MethodSetVolume
	MethodToggleMute
	MethodCycleRepeat
	MethodToggleShuffle
	MethodEnqueue
	MethodRemove
	MethodMove
	MethodClearUpNext
	MethodSearch
	MethodAlbum
	MethodPlaylist
	MethodHome
	MethodExplore
	MethodAccount
	MethodLibrary
	MethodLike
	MethodSignIn
	MethodSignOut
	MethodRefresh
	numMethods
)

// methods describes each Method: its name, for logs, and the args it takes.
var methods = [numMethods]struct {
	name string
	args argKind
}{
	MethodPing:          {"Ping", noArgs},
	MethodHandoff:       {"Handoff", handArgs},
	MethodShutdown:      {"Shutdown", noArgs},
	MethodStatus:        {"Status", noArgs},
	MethodPlay:          {"Play", idsArgs},
	MethodPlayAll:       {"PlayAll", idsArgs},
	MethodToggle:        {"Toggle", noArgs},
	MethodPause:         {"Pause", noArgs},
	MethodResume:        {"Resume", noArgs},
	MethodNext:          {"Next", noArgs},
	MethodPrev:          {"Prev", noArgs},
	MethodSeek:          {"Seek", seekArgs},
	MethodSetVolume:     {"SetVolume", volumeArgs},
	MethodToggleMute:    {"ToggleMute", noArgs},
	MethodCycleRepeat:   {"CycleRepeat", noArgs},
	MethodToggleShuffle: {"ToggleShuffle", noArgs},
	MethodEnqueue:       {"Enqueue", idsArgs},
	MethodRemove:        {"Remove", indexArgs},
	MethodMove:          {"Move", moveArgs},
	MethodClearUpNext:   {"ClearUpNext", noArgs},
	MethodSearch:        {"Search", queryArgs},
	MethodAlbum:         {"Album", refArgs},
	MethodPlaylist:      {"Playlist", refArgs},
	MethodHome:          {"Home", noArgs},
	MethodExplore:       {"Explore", noArgs},
	MethodAccount:       {"Account", noArgs},
	MethodLibrary:       {"Library", noArgs},
	MethodLike:          {"Like", likeArgs},
	MethodSignIn:        {"SignIn", signInArgs},
	MethodSignOut:       {"SignOut", noArgs},
	MethodRefresh:       {"Refresh", partArgs},
}

// Valid reports whether m is a method of this Proto.
func (m Method) Valid() bool { return m > 0 && m < numMethods }

// String returns m's name, or its number when unknown.
func (m Method) String() string {
	if !m.Valid() {
		return "Method(" + strconv.Itoa(int(m)) + ")"
	}
	return methods[m].name
}

// argKind is which of Call's args fields is set; the order is Call's.
type argKind uint8

const (
	noArgs argKind = iota
	idsArgs
	indexArgs
	moveArgs
	seekArgs
	volumeArgs
	queryArgs
	refArgs
	likeArgs
	signInArgs
	partArgs
	handArgs
)

// Validate reports why c is not a call of this Proto: an unknown method,
// more than one args field set, or not the one its method takes.
func (c *Call) Validate() error {
	if !c.Method.Valid() {
		return fmt.Errorf("%w: %s", ErrInvalid, c.Method)
	}
	set := [...]bool{
		idsArgs - 1:    c.IDs != nil,
		indexArgs - 1:  c.Index != nil,
		moveArgs - 1:   c.Move != nil,
		seekArgs - 1:   c.Seek != nil,
		volumeArgs - 1: c.Volume != nil,
		queryArgs - 1:  c.Query != nil,
		refArgs - 1:    c.Ref != nil,
		likeArgs - 1:   c.Like != nil,
		signInArgs - 1: c.SignIn != nil,
		partArgs - 1:   c.Part != nil,
		handArgs - 1:   c.Hand != nil,
	}
	got, n := noArgs, 0
	for i, isSet := range set {
		if isSet {
			got, n = argKind(i+1), n+1
		}
	}
	switch {
	case n > 1:
		return fmt.Errorf("%w: %s with %d args", ErrInvalid, c.Method, n)
	case got != methods[c.Method].args:
		return fmt.Errorf("%w: %s with the wrong args", ErrInvalid, c.Method)
	case c.Part != nil && !c.Part.Part.Valid():
		return fmt.Errorf("%w: %s of part %d", ErrInvalid, c.Method, c.Part.Part)
	}
	return nil
}

// Validate reports why h is not a Hello: it lacks its Proto or Role. It
// accepts any Proto and Role above 0, those of newer peers too, so peers of
// any Proto still greet each other and hand off.
func (h *Hello) Validate() error {
	switch {
	case h.Proto == 0:
		return fmt.Errorf("%w: hello without a proto", ErrInvalid)
	case h.Role == 0:
		return fmt.Errorf("%w: hello without a role", ErrInvalid)
	}
	return nil
}

// Role is who sent a Hello.
type Role uint8

// The roles; add new ones only at the end.
const (
	_ Role = iota
	RoleDesktop
	RoleTUI
	RoleCLI
	RoleDaemon
)

// Part is what the daemon's account fetches (account.Part, which counts
// from 0, plus 1).
type Part uint8

// The parts; add new ones only at the end.
const (
	_ Part = iota
	PartProfile
	PartLibrary
	PartLiked
	numParts
)

// Valid reports whether p is a part of this Proto.
func (p Part) Valid() bool { return p > 0 && p < numParts }

// Repeat is what plays after a song or the queue ends (player.Repeat, which
// counts from 0, plus 1).
type Repeat uint8

// The repeat modes; add new ones only at the end.
const (
	_ Repeat = iota
	RepeatOff
	RepeatAll
	RepeatOne
	numRepeats
)

// Valid reports whether r is a repeat mode of this Proto.
func (r Repeat) Valid() bool { return r > 0 && r < numRepeats }
