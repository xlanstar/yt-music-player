// Package version is the version of ytmd and ytm, which the build sets:
//
//	-ldflags "-X yt-music-player/internal/version.Version=$(jq -r .version mygo.json)"
//
// Desktop reads its own from mygo.json instead. A build without it is a
// development one ("dev"): it never compares versions, so it never hands
// off, and its files get a "-dev" suffix (package ipc), so it leaves an
// installed daemon alone.
package version

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
)

// Dev is the version of a development build.
const Dev = "dev"

// Version is this build's version: semver, or Dev.
var Version = Dev

// IsDev reports whether v is a development build's version; an empty one,
// a build that set nothing, is too.
func IsDev(v string) bool { return v == Dev || v == "" }

// Older reports whether version a precedes b by semver (an optional "v",
// MAJOR.MINOR.PATCH, a pre-release, build metadata ignored). It is false
// when either is a development build or does not parse: such versions are
// never compared.
func Older(a, b string) bool {
	if IsDev(a) || IsDev(b) {
		return false
	}
	va, okA := parse(a)
	vb, okB := parse(b)
	return okA && okB && compare(va, vb) < 0
}

// semver is a parsed version; pre is empty for a release.
type semver struct {
	core [3]uint64
	pre  []string
}

func parse(v string) (semver, bool) {
	v = strings.TrimPrefix(v, "v")
	v, _, _ = strings.Cut(v, "+")
	v, pre, hasPre := strings.Cut(v, "-")
	var s semver
	parts := strings.Split(v, ".")
	if len(parts) != len(s.core) {
		return semver{}, false
	}
	for i, part := range parts {
		n, ok := number(part)
		if !ok {
			return semver{}, false
		}
		s.core[i] = n
	}
	if hasPre {
		s.pre = strings.Split(pre, ".")
		if slices.Contains(s.pre, "") {
			return semver{}, false
		}
	}
	return s, true
}

// number parses a numeric identifier: digits, without a leading zero.
func number(s string) (uint64, bool) {
	if s == "" || (len(s) > 1 && s[0] == '0') || strings.TrimLeft(s, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.ParseUint(s, 10, 64)
	return n, err == nil
}

// compare orders a and b by semver's precedence: -1, 0 or 1.
func compare(a, b semver) int {
	for i := range a.core {
		if a.core[i] != b.core[i] {
			return cmp.Compare(a.core[i], b.core[i])
		}
	}
	switch {
	case len(a.pre) == 0 && len(b.pre) == 0:
		return 0
	case len(a.pre) == 0: // a release follows its pre-releases
		return 1
	case len(b.pre) == 0:
		return -1
	}
	for i := range min(len(a.pre), len(b.pre)) {
		if c := comparePre(a.pre[i], b.pre[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a.pre), len(b.pre))
}

// comparePre orders two pre-release identifiers: numbers by value and
// before words, words by ASCII.
func comparePre(a, b string) int {
	na, numA := number(a)
	nb, numB := number(b)
	switch {
	case numA && numB:
		return cmp.Compare(na, nb)
	case numA:
		return -1
	case numB:
		return 1
	}
	return strings.Compare(a, b)
}
