package api

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// CleanCredentials normalizes a pasted Cookie header, with or without its
// name, on one line or several. The daemon signs in with what it returns;
// a front-end offers to sign in only when that is not "".
func CleanCredentials(pasted string) string {
	const name = "cookie:"
	cleaned := collapseSpace(pasted)
	if len(cleaned) >= len(name) && strings.EqualFold(cleaned[:len(name)], name) {
		cleaned = strings.TrimSpace(cleaned[len(name):])
	}
	return cleaned
}

// collapseSpace is strings.Join(strings.Fields(s), " ") without its 2
// allocations of the pasted size (BenchmarkCleanCredentials): a header
// with no space to collapse comes back as a part of s.
func collapseSpace(s string) string {
	s = strings.TrimSpace(s)
	i := extraSpace(s)
	if i < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	// s is trimmed, so what comes before its first extra space is a field
	// and maybe the one ' ' after it.
	b.WriteString(strings.TrimSuffix(s[:i], " "))
	start := -1
	for i < len(s) {
		c, size := s[i], 1
		isSpace := c < utf8.RuneSelf && asciiSpace[c]
		if c >= utf8.RuneSelf {
			isSpace, size = wideSpace(s[i:])
		}
		switch {
		case !isSpace:
			if start < 0 {
				start = i
			}
		case start >= 0:
			b.WriteByte(' ')
			b.WriteString(s[start:i])
			start = -1
		}
		i += size
	}
	if start >= 0 {
		b.WriteByte(' ')
		b.WriteString(s[start:])
	}
	return b.String()
}

// extraSpace is the index of the first space in s other than one ' '
// after a field, -1 if none.
func extraSpace(s string) int {
	isAfterSpace := false
	for i := 0; i < len(s); {
		c, size := s[i], 1
		isSpace := c < utf8.RuneSelf && asciiSpace[c]
		if c >= utf8.RuneSelf {
			isSpace, size = wideSpace(s[i:])
		}
		if isSpace && (c != ' ' || isAfterSpace) {
			return i
		}
		isAfterSpace = isSpace
		i += size
	}
	return -1
}

var asciiSpace = [utf8.RuneSelf]bool{'\t': true, '\n': true, '\v': true, '\f': true, '\r': true, ' ': true}

// wideSpace reports whether s starts with a non-ASCII space, as
// strings.Fields tells, and the rune's size. Kept out of the loops, whose
// ASCII test, nearly all of a header, then stays inline.
func wideSpace(s string) (isSpace bool, size int) {
	r, size := utf8.DecodeRuneInString(s)
	return unicode.IsSpace(r), size
}
