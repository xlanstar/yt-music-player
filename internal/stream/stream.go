// Package stream resolves a YouTube Music song to an audio URL an audio
// backend can fetch, with the headers it needs.
package stream

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// ErrUnplayable is returned for a song with no audio the resolver can
// give, e.g. removed, region-locked or needing a sign-in.
var ErrUnplayable = errors.New("stream: unplayable")

// Stream is one audio format of a song.
type Stream struct {
	URL string
	// MimeType is as YouTube gives it, e.g. `audio/mp4; codecs="mp4a.40.2"`.
	MimeType string
	Bitrate  int     // bits per second
	Size     int64   // bytes, 0 when unknown
	Seconds  float64 // 0 when unknown
	// Expires is when URL stops working.
	Expires time.Time
	// Header is sent when fetching URL, e.g. the User-Agent of the client
	// it was issued to.
	Header http.Header
}

// Resolver finds a song's audio. Implementations must be safe for
// concurrent use and honor ctx.
type Resolver interface {
	Resolve(ctx context.Context, videoID string) (Stream, error)
	// Forget drops what Resolve cached of a song, e.g. after its URL
	// failed mid-play, so the next Resolve asks anew.
	Forget(videoID string)
}
