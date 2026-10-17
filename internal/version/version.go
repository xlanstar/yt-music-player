// Package version is the version of ytmd and ytm, which the build sets:
//
//	-ldflags "-X yt-music-player/internal/version.Version=$(jq -r .version mygo.json)"
//
// Desktop reads its own from mygo.json instead. A build without it is a
// development one ("dev"): it never compares versions, so it never hands
// off, and its files get a "-dev" suffix (package ipc).
package version

import (
	"slices"
	"strconv"
	"strings"
)

// Dev is the version of a development build.
const Dev = "dev"

// Version is this build's version: MAJOR.MINOR.PATCH, or Dev.
var Version = Dev

// IsDev reports whether v is a development build's version; an empty one,
// a build that set nothing, is too.
func IsDev(v string) bool { return v == Dev || v == "" }

// Older reports whether version a precedes b. Releases are numbered
// MAJOR.MINOR.PATCH (scripts/release.sh); anything else, a development
// build too, never compares.
func Older(a, b string) bool {
	va, okA := parse(a)
	vb, okB := parse(b)
	return okA && okB && slices.Compare(va[:], vb[:]) < 0
}

func parse(v string) (n [3]uint64, ok bool) {
	for i := range n {
		part, rest, found := strings.Cut(v, ".")
		if found != (i < len(n)-1) {
			return n, false
		}
		var err error
		if n[i], err = strconv.ParseUint(part, 10, 64); err != nil {
			return n, false
		}
		v = rest
	}
	return n, true
}
