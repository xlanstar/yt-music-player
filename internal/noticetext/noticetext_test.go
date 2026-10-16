package noticetext

import (
	"strconv"
	"strings"
	"testing"
	"yt-music-player/internal/notice"
	"yt-music-player/locales"
)

// TestNoticeCodes: every code has words in both languages, and they keep
// every argument it takes.
func TestNoticeCodes(t *testing.T) {
	english, chinese := locales.In("en"), locales.In("zh-Hant")
	for code := notice.Code(1); code < notice.NumCodes; code++ {
		m := messages[code]
		if m.say == nil {
			t.Errorf("code %d has no message", code)
			continue
		}
		args := make([]string, m.args)
		for i := range args {
			args[i] = "<arg " + strconv.Itoa(i) + ">"
		}
		en, zh := m.say(english, args, 7), m.say(chinese, args, 7)
		// An error's text is said as is, in neither language.
		isTranslated := code == notice.Reason || zh != en
		if en == "" || !isTranslated {
			t.Errorf("code %d says %q in English and %q in Chinese", code, en, zh)
		}
		for _, arg := range args {
			if !strings.Contains(en, arg) || !strings.Contains(zh, arg) {
				t.Errorf("code %d drops %s: %q, %q", code, arg, en, zh)
			}
		}
	}
}

func TestRender(t *testing.T) {
	for _, tc := range renderCases {
		t.Run(tc.name, func(t *testing.T) {
			title, detail := render(locales.In("en"), tc.n)
			if title != tc.title || detail != tc.detail {
				t.Errorf("said %q, %q; want %q, %q", title, detail, tc.title, tc.detail)
			}
		})
	}
}
