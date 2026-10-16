package api

import (
	"errors"
	"yt-music-player/internal/catalog"
)

// Errors only front-ends make; they never cross the socket.
var (
	ErrDaemonNotFound    = errors.New("api: ytmd not found next to this program; set YTMD_PATH to its path")
	ErrDaemonUnavailable = errors.New("api: ytmd did not answer")
	ErrDisconnected      = errors.New("api: disconnected from ytmd")
	ErrProtoMismatch     = errors.New("api: ytmd speaks another protocol version")
)

// ErrInternal is what a front-end reads for any failure in the daemon
// without an ErrorCode of its own.
var ErrInternal = errors.New("api: ytmd failed")

// ErrorCode names an error a front-end tells apart (errors.Is). Values
// travel as numbers: add new ones only at the end. A code unknown to the
// receiver reads as CodeInternal.
type ErrorCode uint16

// The error codes, in the order Encode tries them.
const (
	_ ErrorCode = iota
	CodeNotFound
	CodeSignedOut
	CodeInvalidCredentials
	CodeInternal
	numCodes
)

// sentinels are what each code stands for, on either side.
var sentinels = [numCodes]error{
	CodeNotFound:           catalog.ErrNotFound,
	CodeSignedOut:          catalog.ErrSignedOut,
	CodeInvalidCredentials: catalog.ErrInvalidCredentials,
	CodeInternal:           ErrInternal,
}

// Valid reports whether c is a code of this Proto.
func (c ErrorCode) Valid() bool { return c > 0 && c < numCodes }

// Error is an error on the wire. Bootstrap: never changes.
type Error struct {
	Code ErrorCode
	// Detail is text safe to show: never cookies, headers, visitorData or
	// stream URLs.
	Detail string
}

// Encode returns err as an Error, nil for nil. Only invalid credentials
// keep their text as Detail, which the sign-in form shows; an Internal
// error says nothing, as its text may hold anything: log it instead.
func Encode(err error) *Error {
	if err == nil {
		return nil
	}
	for code := CodeNotFound; code < CodeInternal; code++ {
		if errors.Is(err, sentinels[code]) {
			e := &Error{Code: code}
			if code == CodeInvalidCredentials {
				e.Detail = err.Error()
			}
			return e
		}
	}
	return &Error{Code: CodeInternal}
}

// Err returns e as an error, nil for nil: errors.Is finds its code's
// sentinel (ErrInternal for a code unknown here), and its text is the
// sentinel's, then Detail. Front-ends wrap it as they need.
func (e *Error) Err() error {
	if e == nil {
		return nil
	}
	code := e.Code
	if !code.Valid() {
		code = CodeInternal
	}
	return &decoded{sentinel: sentinels[code], detail: e.Detail}
}

// decoded is an Error received.
type decoded struct {
	sentinel error
	detail   string
}

func (d *decoded) Error() string {
	if d.detail == "" {
		return d.sentinel.Error()
	}
	return d.sentinel.Error() + ": " + d.detail
}

func (d *decoded) Unwrap() error { return d.sentinel }
