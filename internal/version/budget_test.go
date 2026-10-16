package version

import (
	"testing"
	"yt-music-player/internal/budget"
)

func TestBudgetOlder(t *testing.T) {
	for _, tc := range olderCases {
		t.Run(tc.name, func(t *testing.T) {
			budget.Allocs(t, tc.limit, 100, func() { sinkBool = Older(tc.a, tc.b) })
		})
	}
}
