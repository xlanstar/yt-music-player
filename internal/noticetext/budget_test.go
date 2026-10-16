package noticetext

import (
	"testing"
	"yt-music-player/internal/budget"
	"yt-music-player/locales"
)

func TestBudgetRender(t *testing.T) {
	english := locales.In("en")
	for _, tc := range renderCases {
		t.Run(tc.name, func(t *testing.T) {
			budget.Allocs(t, tc.limit, 100, func() { sinkTitle, sinkDetail = render(english, tc.n) })
		})
	}
}
