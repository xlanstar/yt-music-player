package noticetext

import (
	"testing"
	"yt-music-player/internal/notice"
	"yt-music-player/locales"
)

// Sinks keep the compiler from optimizing measured calls away.
var sinkTitle, sinkDetail string

// renderCases are notices with no argument, with one in the title and a
// count in the detail, with a reason, and with an argument in each.
var renderCases = []struct {
	name          string
	n             notice.Notice
	title, detail string
	limit         float64
}{
	{
		name:  "plain",
		n:     notice.Notice{Kind: notice.Info, Title: notice.SignedOut},
		title: "Signed out",
		// 0 allocs today.
		limit: 0,
	},
	{
		name: "counted",
		n: notice.Notice{
			Kind: notice.Failure, Title: notice.Unplayable, Detail: notice.PlaybackStoppedAfter,
			Args: []string{"Dead Air"}, N: 3, Topic: notice.Playback,
		},
		title:  "“Dead Air” isn’t available",
		detail: "Playback stopped: 3 songs in a row couldn’t play.",
		// 26 allocs today, all the catalog's formatting.
		limit: 30,
	},
	{
		name: "reason",
		n: notice.Notice{
			Kind: notice.Failure, Title: notice.UnlikeFailed, Detail: notice.Reason,
			Args: []string{"Dead Air", "quota exceeded"},
		},
		title:  "Couldn’t unlike “Dead Air”",
		detail: "quota exceeded",
		// 10 allocs today, all the catalog's formatting of the title.
		limit: 12,
	},
	{
		name: "fallback",
		n: notice.Notice{
			Kind: notice.Failure, Title: notice.ThemeReadFailed, Detail: notice.ThemeFallback,
			Args: []string{"neon.json", "type.title: 400 is out of 6 to 96"}, Topic: notice.Theme + "neon.json",
		},
		title:  "Couldn’t read the theme neon.json",
		detail: "type.title: 400 is out of 6 to 96. The default theme shows instead.",
		// 20 allocs today, the catalog's formatting of both.
		limit: 23,
	},
}

func BenchmarkRender(b *testing.B) {
	english := locales.In("en")
	for _, tc := range renderCases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkTitle, sinkDetail = render(english, tc.n)
			}
		})
	}
}
