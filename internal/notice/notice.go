// Package notice holds messages for the user produced outside a build
// (by the session and playback). The app shows them on its next build, as
// a toast or in the sign-in sheet, all through one path.
package notice

// Kind is the type of a Notice; it decides how the notice is shown.
type Kind uint8

// The kinds of notice.
const (
	Info         Kind = iota // e.g. "Signed out"
	Failure                  // Detail says why
	NeedsAccount             // offer to sign in, if the source supports it
	SignedIn                 // the submitted credentials worked
	SignInFailed             // Detail says why
)

// Notice is a message for the user.
type Notice struct {
	Kind   Kind
	Title  string
	Detail string
	// Topic, when set, replaces a visible notice of the same topic instead
	// of stacking, so a run of similar failures shows only the last.
	Topic string
}

// Notice topics.
const (
	Playback = "playback" // a song failed to play
	SignIn   = "sign-in"  // something needs an account
)
