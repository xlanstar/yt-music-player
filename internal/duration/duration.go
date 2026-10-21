// Package duration says lengths of time as the app, ytm's TUI and its
// commands show them, so all three say the same: a song's length or
// position as a clock, a collection's length in minutes. It knows no UI.
package duration

import "strconv"

// Clock says seconds as m:ss, or h:mm:ss from an hour.
func Clock(seconds float64) string {
	s := max(int(seconds), 0)
	h, m := s/3600, s/60
	b := make([]byte, 0, len("100:00:00"))
	if h > 0 {
		m %= 60
		b = strconv.AppendInt(b, int64(h), 10)
		b = append(b, ':')
		if m < 10 {
			b = append(b, '0')
		}
	}
	b = strconv.AppendInt(b, int64(m), 10)
	b = append(b, ':')
	if s%60 < 10 {
		b = append(b, '0')
	}
	b = strconv.AppendInt(b, int64(s%60), 10)
	return string(b)
}

// Minutes rounds seconds to the nearest minute.
func Minutes(seconds int) int {
	return (seconds + 30) / 60
}
