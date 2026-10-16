// Package api is the protocol between ytmd and its front-ends (Desktop, the
// TUI, the CLI): the Frame each side sends over the socket, the methods,
// events and error codes. It holds only types and pure functions; package
// ipc carries the frames.
//
// Frames travel as one gob stream per direction. Gob matches struct fields
// by name and skips what the receiver lacks, so adding a field needs no new
// Proto; changing what a field means does. Never change a field's type:
// gob then refuses every Frame, the bootstrap ones too, so the peers could
// no longer even hand off. Give the new type a new field name instead.
//
// Gob omits zero values, so a receiver zeroes its Frame before each decode,
// and every enum starts at 1: a 0 is a missing field, rejected. No field is
// an interface (nothing is gob.Register'ed), a func or a chan.
//
// The bootstrap messages never change, in any Proto: Frame.ID, Hello,
// Reply.Err, Error, HandoffArgs, HandoffEvent and the numbers of
// MethodPing, MethodHandoff and MethodShutdown. With them a front-end hands
// off to, or stops, a daemon of any Proto (TestBootstrapFrozen).
package api

import (
	"time"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/notice"
)

// Proto is the protocol's version, sent in Hello. Peers of different
// Protos exchange only the bootstrap messages.
const Proto uint16 = 1

// Frame is one message on the socket; exactly one of Hello, Call, Reply,
// Cancel and Event is set.
type Frame struct {
	// ID numbers a front-end's requests from 1; a Reply or Cancel carries
	// its request's. Events are 0.
	ID     uint32
	Hello  *Hello
	Call   *Call
	Reply  *Reply
	Cancel bool // cancels the request ID
	Event  *Event
}

// Hello is each side's first frame.
type Hello struct {
	Proto   uint16
	Version string // semver, or "dev"
	Role    Role
	// Subscribe asks for events: Desktop and the TUI do, the CLI not.
	Subscribe bool
	PID       int
}

// Call is a request; at most one args field is set, the one its Method
// takes (Validate).
type Call struct {
	Method Method
	IDs    *IDsArgs    // Play, PlayAll, Enqueue
	Index  *IndexArgs  // Remove
	Move   *MoveArgs   // Move
	Seek   *SeekArgs   // Seek
	Volume *VolumeArgs // SetVolume
	Query  *QueryArgs  // Search
	Ref    *RefArgs    // Album, Playlist
	Like   *LikeArgs   // Like
	SignIn *SignInArgs // SignIn
	Part   *PartArgs   // Refresh
	Hand   *HandoffArgs
}

// Reply answers a Call: Err, or at most one result. Methods without a
// result reply with neither.
type Reply struct {
	Err      *Error
	Results  *catalog.Results
	Album    *AlbumReply
	Playlist *PlaylistReply
	Home     *catalog.Home
	Explore  *catalog.Explore
	Account  *catalog.Account
	Library  *catalog.Library
	Status   *Status
}

// Event is what changed in the daemon, sent to subscribed connections:
// first a full snapshot of each, then changes. Several fields may be set.
type Event struct {
	State *State
	Queue *Queue
	// Songs describe the songs newly in the queue, all of them in a
	// snapshot.
	Songs   []catalog.Song
	Likes   *Likes
	Account *AccountEvent
	Library *catalog.Library
	Failed  *FailedEvent
	Notice  *notice.Notice
	Handoff *HandoffEvent
}

// IDsArgs are songs to play or enqueue.
type IDsArgs struct {
	IDs []string
	// Start is the index in IDs Play starts at.
	Start int
	// Next enqueues after the current song rather than at the end.
	Next bool
}

// IndexArgs name a song by its index in the queue.
type IndexArgs struct{ Index int }

// MoveArgs move the queue's song at From to To.
type MoveArgs struct{ From, To int }

// SeekArgs seek to Seconds into the current song.
type SeekArgs struct{ Seconds float64 }

// VolumeArgs set the volume, 0 to 1.
type VolumeArgs struct{ Volume float64 }

// QueryArgs are a search.
type QueryArgs struct{ Query string }

// RefArgs name an album or playlist by its ID.
type RefArgs struct{ ID string }

// LikeArgs like or unlike a song.
type LikeArgs struct {
	ID    string
	Liked bool
}

// SignInArgs carry credentials as pasted. Never log them: String hides
// them from fmt.
type SignInArgs struct{ Credentials string }

// String implements fmt.Stringer without the credentials.
func (SignInArgs) String() string { return "SignInArgs{…}" }

// GoString implements fmt.GoStringer without the credentials.
func (SignInArgs) GoString() string { return "api.SignInArgs{…}" }

// PartArgs name what Refresh fetches anew.
type PartArgs struct{ Part Part }

// HandoffArgs ask the daemon to save and exit for a newer one, which the
// caller starts within Wait. Bootstrap: never changes.
type HandoffArgs struct{ Wait time.Duration }

// AlbumReply is an album with its tracks in order.
type AlbumReply struct {
	Album catalog.Album
	Songs []catalog.Song
}

// PlaylistReply is a playlist with its songs in order.
type PlaylistReply struct {
	Playlist catalog.Playlist
	Songs    []catalog.Song
}

// Status is a snapshot for `ytm status`.
type Status struct {
	Version string
	Proto   uint16
	State   State
	// Song is the current song; zero with an empty queue.
	Song catalog.Song
	// Length is the number of songs in the queue; State.Index is the
	// current one's.
	Length   int
	SignedIn bool
	Account  catalog.Account
}

// State is the playback state, sent whole on every change: play, pause,
// seek, a new song, a stall starting or ending, volume, mute, repeat,
// shuffle; never on a timer while playing.
type State struct {
	Playing bool
	// Waiting reports a song loading or stalled: Position stands still,
	// though Playing.
	Waiting bool
	// Position is the seconds into the current song when sent; a receiver
	// moves it on with its own monotonic clock while Playing and not
	// Waiting.
	Position float64
	Duration float64 // seconds; 0 when unknown
	Volume   float64 // 0 to 1
	Muted    bool
	Repeat   Repeat
	Shuffled bool
	// Current is the current song's ID and Index its index in the queue;
	// "" and 0 with an empty queue.
	Current string
	Index   int
}

// Queue is the whole queue, in play order (shuffled when shuffling).
type Queue struct{ IDs []string }

// Likes changes the liked songs (account.Likes). With Full, IDs are all of
// them, newest first, and Songs describe them; otherwise Added and Removed
// are the likes that went through since. Never coalesced.
type Likes struct {
	Full           bool
	IDs            []string
	Songs          []catalog.Song
	Added, Removed []string
}

// AccountEvent is who is signed in (account.Status). Switched reports a
// sign-in: what was fetched before is stale.
type AccountEvent struct {
	Account  catalog.Account
	SignedIn bool
	Switched bool
}

// FailedEvent is a fetch of the account, library or likes that failed
// (account.Failure); Refresh with its Part retries it.
type FailedEvent struct {
	Part Part
	Err  *Error
}

// HandoffEvent tells the other connections that the front-end of PID By
// hands off: within Wait (relative, so each side reads its own clock) they
// reconnect but never start a daemon. Bootstrap: never changes.
type HandoffEvent struct {
	By   int
	Wait time.Duration
}
