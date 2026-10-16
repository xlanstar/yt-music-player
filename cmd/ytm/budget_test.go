package main

import (
	"io"
	"testing"
	"yt-music-player/internal/budget"
)

func TestBudgetStatusJSON(t *testing.T) {
	for _, tc := range statusCases {
		t.Run(tc.name, func(t *testing.T) {
			budget.Allocs(t, tc.jsonLimit, 100, func() { _ = writeJSON(io.Discard, statusOf(&tc.status)) })
		})
	}
}

func TestBudgetStatusText(t *testing.T) {
	for _, tc := range statusCases {
		t.Run(tc.name, func(t *testing.T) {
			budget.Allocs(t, tc.limit, 100, func() { sinkString = statusText(&tc.status) })
		})
	}
}
