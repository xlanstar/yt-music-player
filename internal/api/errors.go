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

// IsValid reports whether c is a code of this Proto.
func (c ErrorCode) IsValid() bool { return c > 0 && c < numCodes }

// Error is an error on the wire, and the error a front-end reads:
// errors.Is finds its code's sentinel (ErrInternal for a code unknown
// here), and its text is the sentinel's, then Detail. Bootstrap: never
// changes.
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

// Err returns e as an error, nil for nil (not an error holding a nil
// *Error).
func (e *Error) Err() error {
	if e == nil {
		return nil
	}
	return e
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return e.Unwrap().Error()
	}
	return e.Unwrap().Error() + ": " + e.Detail
}

// Unwrap returns the sentinel of e's code.
func (e *Error) Unwrap() error {
	if !e.Code.IsValid() {
		return ErrInternal
	}
	return sentinels[e.Code]
}
