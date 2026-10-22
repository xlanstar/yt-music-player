package api

import (
	"strings"
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

func TestBudgetCleanCredentials(t *testing.T) {
	// 1 alloc today: the header collapsed onto one line.
	budget.Allocs(t, 1, 100, func() { sinkString = CleanCredentials(cookieHeader) })
	// 0 allocs today: on one line already, the header is its own part.
	oneLine := strings.Join(strings.Fields(cookieHeader), " ")
	budget.Allocs(t, 0, 100, func() { sinkString = CleanCredentials(oneLine) })
}

func TestBudgetSplit(t *testing.T) {
	for _, tc := range partCases {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.reply()
			undo := unsplit(r)
			budget.Allocs(t, tc.split, 20, func() {
				undo()
				sinkAhead = r.Split()
			})
		})
	}
}

func TestBudgetJoin(t *testing.T) {
	for _, tc := range partCases {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.reply()
			ahead := r.Split()
			budget.Allocs(t, tc.join, 20, func() {
				r.Join(ahead)
				unjoin(r)
			})
		})
	}
}
