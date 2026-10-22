// Package notice holds messages for the user produced outside a build
// (by the session, playback and the settings). The app shows them on its
// next build, as a toast or in the sign-in sheet, all through one path.
//
// A Notice holds no text, only plain data: codes naming its messages, and
// their arguments. Whoever shows it says them in the user's language (see
// noticetext), so its producers need no catalog of messages.
package notice

// Kind is the type of a Notice; it decides how the notice is shown.
type Kind uint8

// The kinds of notice.
const (
	KindInfo         Kind = iota // e.g. "Signed out"
	KindFailure                  // Detail says why
	KindNeedsAccount             // offer to sign in, if the source supports it
	KindSignedIn                 // the submitted credentials worked
	KindSignInError              // Detail says why
)

// Code names a message. 0 is none.
type Code uint16

// The messages, in English. Braces hold their arguments: {N} is
// Notice.N, the others come from Args.
const (
	_                    Code = iota
	NoAudio                   // Can’t play audio
	PlayFailed                // Couldn’t play “{title}”
	Unplayable                // “{title}” isn’t available
	PlaybackStopped           // Playback stopped.
	PlaybackStoppedAfter      // Playback stopped: {N} songs in a row couldn’t play.
	Skipped                   // Skipped to the next song.
	LikeFailed                // Couldn’t like “{title}”
	UnlikeFailed              // Couldn’t unlike “{title}”
	LikeFailedSong            // Couldn’t like the song
	UnlikeFailedSong          // Couldn’t unlike the song
	SignInToLike              // Sign in to like songs
	SignInFailed              // Couldn’t sign in
	SignedInAs                // Signed in as {name}
	SignOutFailed             // Couldn’t sign out
	SignedOut                 // Signed out
	Reason                    // {an error’s text}, as is
	ControlFailed             // Couldn’t change playback
	AutostartFailed           // Couldn’t change starting at login
	TerminalFailed            // Couldn’t change ytm in the terminal
	SettingsSaveFailed        // Couldn’t save your settings
	SettingsReadFailed        // Couldn’t read your settings
	ThemesReadFailed          // Couldn’t read your themes
	ThemeReadFailed           // Couldn’t read the theme {name}
	ThemeFallback             // {reason}. The default theme shows instead.
	LayoutFailed              // Couldn’t use your layout

	// NumCodes is the length of a table indexed by Code.
	NumCodes
)

// Notice is a message for the user.
type Notice struct {
	Kind Kind
	// Title says what happened; Detail, when set, why or what followed.
	Title, Detail Code
	// Args are Title's string arguments, then Detail's.
	Args []string
	// N is the count of a message that has one.
	N int
	// Topic, when set, replaces a visible notice of the same topic instead
	// of stacking, so a run of similar failures shows only the last.
	Topic Topic
}

// Topic groups notices that replace one another.
type Topic string

// Notice topics.
const (
	TopicPlayback Topic = "playback" // a song failed to play
	TopicSignIn   Topic = "sign-in"  // something needs an account
	TopicControl  Topic = "control"  // the daemon failed a control or did not answer

	TopicSettingsSave Topic = "settings-save" // the settings failed to save
	TopicSettingsRead Topic = "settings-read" // the settings failed to read
	TopicThemes       Topic = "themes"        // the user's themes failed to read
	TopicTheme        Topic = "theme:"        // and a theme's file: it failed to read
	TopicLayout       Topic = "layout"        // the user's layout failed to apply
)
