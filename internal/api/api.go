// Package api is the protocol between ytmd and its front-ends: the Frame
// each side sends, its methods, events and error codes; types and pure
// functions only (package ipc carries the frames).
//
// Frames are one gob stream per direction. Gob skips fields the receiver
// lacks, so adding a field needs no new Proto, changing its meaning does.
// Never change a field's type, which makes gob refuse every Frame: give the
// new type a new name. Gob omits zero values, so a receiver zeroes each
// Frame before decoding, and every enum it carries, this package's or
// another's (notice.Kind, catalog.ItemKind), starts at 1: a 0 is a missing
// field. No field is an interface, a func or a chan.
//
// Frame.ID, Hello, Reply.Err, Error, HandoffArgs, HandoffEvent and the
// numbers of Ping, Handoff and Shutdown never change, in any Proto, so a
// front-end can hand off to, or stop, any daemon (TestBootstrapFrozen).
package api

import (
	"time"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/notice"
)

// Proto is the protocol's version, sent in Hello. Peers of different
// Protos exchange only the bootstrap messages.
const Proto uint16 = 2

// Frame is one message on the socket; exactly one of Hello, Call, Reply,
// Ahead, Cancel and Event is set.
type Frame struct {
	Hello *Hello
	Call  *Call
	Reply *Reply
	Event *Event
	// Ahead is a part of the long lists of the Call or Reply of ID, which
	// follows its parts (Split).
	Ahead *Ahead
	// ID numbers a front-end's requests from 1; a Reply, Ahead or Cancel
	// carries its request's. Events are 0.
	ID     uint32
	Cancel bool // cancels the request ID
}

// Hello is each side's first frame.
type Hello struct {
	Version string // MAJOR.MINOR.PATCH, or "dev"
	PID     int
	Proto   uint16
	Role    Role
	// Subscribe asks for events: Desktop and the TUI do, the CLI not.
	Subscribe bool
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
	Hand   *HandoffArgs
}

// Reply answers a Call: Err, or at most one result. Methods without a
// result reply with neither. A long result's lists may come first, in
// frames of the request's ID with Ahead alone.
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
	// snapshot; too many for one frame, they come ahead in events of their own.
	Songs   []catalog.Song
	Likes   *Likes
	Account *AccountEvent
	Failed  *FailedEvent
	Notice  *notice.Notice
	Handoff *HandoffEvent
}

// IDsArgs are songs to play or enqueue.
type IDsArgs struct {
	IDs []string
	// Songs describe the songs of IDs the caller knows, so the daemon,
	// which keeps no catalog of its own, can show and announce them. Too
	// many for one frame, they come ahead (Frame.Ahead).
	Songs []catalog.Song
	// Start is the index in IDs Play and PlayAll start at.
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
	// Song describes the song liked when the caller knows it, so that the
	// other front-ends' Liked music shows it (Likes.Songs); nil otherwise.
	// Its ID is ID. An unlike needs none.
	Song *catalog.Song
}

// SignInArgs carry credentials as pasted. Never log them: String hides
// them from fmt.
type SignInArgs struct{ Credentials string }

// String implements fmt.Stringer without the credentials.
func (SignInArgs) String() string { return "SignInArgs{…}" }

// GoString implements fmt.GoStringer without the credentials.
func (SignInArgs) GoString() string { return "api.SignInArgs{…}" }

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
	State   State
	// Song is the current song; zero with an empty queue.
	Song catalog.Song
	// Length is the queue size; State.Index identifies the current song.
	Length   int
	Account  catalog.Account
	Proto    uint16
	SignedIn bool
}

// State is the playback state, sent whole on each change, never on a
// timer.
type State struct {
	// Current is the current song's ID and Index its index in the queue;
	// "" and 0 with an empty queue.
	Current string
	Index   int
	// Position is seconds into the song when sent; receivers advance it with a
	// monotonic clock while Playing and not Waiting.
	Position float64
	Duration float64 // seconds; 0 when unknown
	Volume   float64 // 0 to 1
	Playing  bool
	// Waiting freezes Position while a Playing song loads or stalls.
	Waiting  bool
	Muted    bool
	Repeat   Repeat
	Shuffled bool
}

// Queue is the whole queue, in play order (shuffled when shuffling).
type Queue struct{ IDs []string }

// Likes are likes that went through (account.Likes), which each front-end
// applies to the liked songs it fetched; Songs describe those of Added
// whose Like described them (LikeArgs.Song). Never coalesced.
type Likes struct {
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

// FailedEvent is a fetch of who is signed in that failed (account's
// Failed); Refresh retries it.
type FailedEvent struct{ Err *Error }

// HandoffEvent tells the other connections that the front-end of PID By
// hands off: within Wait (relative, so each side reads its own clock) they
// reconnect but never start a daemon. Bootstrap: never changes.
type HandoffEvent struct {
	By   int
	Wait time.Duration
}
