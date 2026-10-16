package autostart

import (
	"strconv"
	"strings"
	"testing"
)

// Sinks keep the compiler from optimizing measured calls away.
var (
	sinkBytes  []byte
	sinkString string
)

// Entries render once per switch or start, never per frame: these guard
// against a renderer gone quadratic, not against a few allocations.

// renderCases are the renderers over the paths each platform's installs
// give; allocs is each one's budget.
var renderCases = []struct {
	name   string
	render func(string) []byte
	ytmd   string
	allocs float64
}{
	// 3 allocs today: the buffer, sized up front, the path as bytes for
	// xml.EscapeText, and the buffer's header, which escapes into it.
	{"plist", renderPlist, appYtmd, 3},
	// 3 allocs today.
	{"plist-escaped", renderPlist, "/Users/a & b/<YT>/ytmd", 3},
	// 1 alloc today.
	{"unit", renderUnit, "/opt/yt-music-player/ytmd", 1},
	// 1 alloc today; quoting fits the room left for it.
	{"unit-quoted", renderUnit, "/home/me/My Apps/yt-music-player.app/ytmd", 1},
}

func BenchmarkRender(b *testing.B) {
	for _, tc := range renderCases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkBytes = tc.render(tc.ytmd)
			}
		})
	}
}

// pathCases are user Paths without our entry; the long one is a
// developer's, of 60 entries. Adding appends ours, removing takes it off
// the end again; add and remove are their budgets.
var pathCases = []struct {
	name        string
	others      string
	add, remove float64
}{
	// 1 alloc today, and 1: the joined list; the entries kept, of which
	// strings.Join returns the one as it is.
	{"short", `%USERPROFILE%\AppData\Local\Microsoft\WindowsApps`, 1, 1},
	// 1 alloc today, and 8: the kept entries grow as they are found.
	{"long-60", longPath(60), 1, 10},
}

func longPath(n int) string {
	entries := make([]string, n)
	for i := range entries {
		entries[i] = `C:\Users\me\AppData\Local\Programs\Tool ` + strconv.Itoa(i) + `\bin`
	}
	return strings.Join(entries, ";")
}

func BenchmarkEditPath(b *testing.B) {
	for _, tc := range pathCases {
		b.Run("add/"+tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkString = addPathEntry(tc.others, installDir)
			}
		})
		with := tc.others + ";" + installDir
		b.Run("remove/"+tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkString = removePathEntry(with, installDir)
			}
		})
	}
}
