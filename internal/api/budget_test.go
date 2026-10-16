package api

import (
	"testing"
	"yt-music-player/internal/budget"
)

func TestBudgetEncode(t *testing.T) {
	for _, tc := range encodeCases {
		t.Run(tc.name, func(t *testing.T) {
			encode, _ := steadyEncoder(t, tc.frame())
			budget.Allocs(t, tc.limit, 20, encode)
		})
	}
}

func TestBudgetValidate(t *testing.T) {
	call := &Call{Method: MethodEnqueue, IDs: &IDsArgs{IDs: []string{"a"}}}
	// 0 allocs today.
	budget.Allocs(t, 0, 100, func() { _ = call.Validate() })
}
