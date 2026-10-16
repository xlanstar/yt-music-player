// Package noticetext says notices in the user's language. Their producers
// (playback, session) name messages by code and need no catalog of
// messages; whatever shows notices says them through Render, so all say
// the same words.
package noticetext

import (
	"yt-music-player/internal/notice"
	"yt-music-player/locales"

	"github.com/egoist/mygo/i18n"
)

// Render says n's title and detail in the language the app shows; detail
// is "" when n has none.
func Render(n notice.Notice) (title, detail string) {
	return render(locales.In(i18n.Locale()), n)
}

func render(msgs locales.Messages, n notice.Notice) (title, detail string) {
	title, args := say(msgs, n.Title, n.Args, n.N)
	detail, _ = say(msgs, n.Detail, args, n.N)
	return title, detail
}

// say says code, taking its arguments off the front of args, and returns
// the rest.
func say(msgs locales.Messages, code notice.Code, args []string, count int) (text string, rest []string) {
	if code == 0 {
		return "", args
	}
	m := messages[code]
	return m.say(msgs, args[:m.args], count), args[m.args:]
}

// message says a code that takes args string arguments.
type message struct {
	args int
	say  func(msgs locales.Messages, args []string, count int) string
}

// messages is indexed by code; TestNoticeCodes finds any left out.
var messages = [notice.NumCodes]message{
	notice.NoAudio:              plain(locales.Messages.NoticeNoAudio),
	notice.PlayFailed:           named(locales.Messages.NoticePlayFailed),
	notice.Unplayable:           named(locales.Messages.NoticeUnplayable),
	notice.PlaybackStopped:      plain(locales.Messages.NoticePlaybackStopped),
	notice.PlaybackStoppedAfter: counted(locales.Messages.NoticePlaybackStoppedAfter),
	notice.Skipped:              plain(locales.Messages.NoticeSkipped),
	notice.LikeFailed:           named(locales.Messages.NoticeLikeFailed),
	notice.UnlikeFailed:         named(locales.Messages.NoticeUnlikeFailed),
	notice.LikeFailedSong:       plain(locales.Messages.NoticeLikeFailedSong),
	notice.UnlikeFailedSong:     plain(locales.Messages.NoticeUnlikeFailedSong),
	notice.SignInToLike:         plain(locales.Messages.NoticeSignInToLike),
	notice.SignInFailed:         plain(locales.Messages.NoticeSignInFailed),
	notice.SignedInAs:           named(locales.Messages.SignedInAs),
	notice.SignOutFailed:        plain(locales.Messages.NoticeSignOutFailed),
	notice.SignedOut:            plain(locales.Messages.NoticeSignedOut),
	notice.ControlFailed:        plain(locales.Messages.NoticeControlFailed),
	notice.AutostartFailed:      plain(locales.Messages.NoticeAutostartFailed),
	notice.TerminalFailed:       plain(locales.Messages.NoticeTerminalFailed),
	// An error's text is in no language of ours.
	notice.Reason: {args: 1, say: func(_ locales.Messages, args []string, _ int) string { return args[0] }},
}

func plain(say func(locales.Messages) string) message {
	return message{say: func(msgs locales.Messages, _ []string, _ int) string { return say(msgs) }}
}

func named(say func(locales.Messages, string) string) message {
	return message{args: 1, say: func(msgs locales.Messages, args []string, _ int) string { return say(msgs, args[0]) }}
}

func counted(say func(locales.Messages, int) string) message {
	return message{say: func(msgs locales.Messages, _ []string, count int) string { return say(msgs, count) }}
}
