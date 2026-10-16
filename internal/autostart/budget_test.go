package autostart

import (
	"testing"
	"yt-music-player/internal/budget"
)

func TestBudgetRender(t *testing.T) {
	for _, tc := range renderCases {
		t.Run(tc.name, func(t *testing.T) {
			budget.Allocs(t, tc.allocs, 100, func() { sinkBytes = tc.render(tc.ytmd) })
		})
	}
}

func TestBudgetEditPath(t *testing.T) {
	for _, tc := range pathCases {
		with := tc.others + ";" + installDir
		t.Run(tc.name, func(t *testing.T) {
			budget.Allocs(t, tc.add, 100, func() { sinkString = addPathEntry(tc.others, installDir) })
			budget.Allocs(t, tc.remove, 100, func() { sinkString = removePathEntry(with, installDir) })
		})
	}
}
