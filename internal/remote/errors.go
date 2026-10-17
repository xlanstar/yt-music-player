package remote

import (
	"errors"
	"yt-music-player/internal/api"
)

// Error is a failure the daemon replied with. errors.Is finds its code's
// sentinel (catalog.ErrNotFound, catalog.ErrSignedOut,
// catalog.ErrInvalidCredentials, api.ErrInternal).
type Error struct {
	// Code is CodeInternal for a code unknown to this build.
	Code api.ErrorCode
	// Detail is text safe to show, often "".
	Detail string
}

// Error returns the sentinel's text, then Detail.
func (e *Error) Error() string {
	text := sentinels[e.Code].Error()
	if e.Detail == "" {
		return text
	}
	return text + ": " + e.Detail
}

// Unwrap returns the sentinel of e's code.
func (e *Error) Unwrap() error { return sentinels[e.Code] }

// sentinels are api's table, which it keeps to itself, read back through
// its decoding.
var sentinels = func() map[api.ErrorCode]error {
	table := map[api.ErrorCode]error{}
	for code := api.ErrorCode(1); code.Valid(); code++ {
		table[code] = errors.Unwrap((&api.Error{Code: code}).Err())
	}
	return table
}()

// Decode returns e as an *Error, nil for nil.
func Decode(e *api.Error) error {
	if e == nil {
		return nil
	}
	code := e.Code
	if !code.Valid() {
		code = api.CodeInternal
	}
	return &Error{Code: code, Detail: e.Detail}
}
