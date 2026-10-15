// Package auth signs InnerTube requests with a signed-in browser session's
// cookies (ytmusicapi's browser auth) and keeps them across launches.
//
// Credentials are secrets: never log them or put them in an error.
package auth

import (
	"crypto/sha1" //nolint:gosec // SAPISIDHASH is SHA-1, see SAPISIDHash
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// defaultOrigin signs SAPISIDHASH for requests without an Origin header.
const defaultOrigin = "https://music.youtube.com"

// ErrInvalid is returned for input that holds no usable session cookie.
var ErrInvalid = errors.New("auth: no SAPISID cookie; copy the Cookie header of a signed-in music.youtube.com request")

// Credentials are a browser session's cookies. The zero value is none.
type Credentials struct {
	cookie  string
	sapisid string
	// authUser picks the account among those signed in to the browser.
	authUser string
}

// Parse reads credentials leniently from what a user copies out of devtools:
// a Cookie header value, a "Cookie: …" line, a block of request headers
// (Chrome, Firefox, Safari, or curl "-H" arguments), or ytmusicapi's
// browser.json. Without X-Goog-AuthUser the browser's first account is used.
func Parse(pasted string) (Credentials, error) {
	pasted = strings.TrimSpace(strings.TrimPrefix(pasted, "\ufeff"))
	cookie, authUser, found := fromJSON(pasted)
	if !found {
		cookie, authUser, found = fromHeaders(pasted)
	}
	if !found {
		cookie = unquote(pasted)
		if name, value, ok := strings.Cut(cookie, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "cookie") {
			cookie = value
		}
	}
	return newCredentials(cookie, authUser)
}

func newCredentials(cookie, authUser string) (Credentials, error) {
	// One Builder: a string per pair, then their join, took about one
	// allocation a cookie (BenchmarkParse).
	var pairs strings.Builder
	pairs.Grow(len(cookie) + strings.Count(cookie, ";"))
	jar := map[string]string{}
	for part := range strings.SplitSeq(cookie, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !ok || !validName(name) || !validValue(value) {
			continue
		}
		if _, isDup := jar[name]; isDup {
			continue
		}
		jar[name] = value
		if pairs.Len() > 0 {
			pairs.WriteString("; ")
		}
		pairs.WriteString(name)
		pairs.WriteByte('=')
		pairs.WriteString(value)
	}
	// ytmusicapi signs with __Secure-3PAPISID; browsers set both to the
	// same value, and either alone signs requests.
	sapisid := jar["__Secure-3PAPISID"]
	if sapisid == "" {
		sapisid = jar["SAPISID"]
	}
	if sapisid == "" {
		return Credentials{}, ErrInvalid
	}
	return Credentials{cookie: pairs.String(), sapisid: sapisid, authUser: cleanAuthUser(authUser)}, nil
}

// cleanAuthUser returns authUser as a plain account index, "0" (the
// browser's first account) when it is not one.
func cleanAuthUser(authUser string) string {
	index, err := strconv.Atoi(strings.TrimSpace(authUser))
	if err != nil || index < 0 || index >= 100 {
		return "0"
	}
	return strconv.Itoa(index)
}

// fromJSON reads ytmusicapi's browser.json, a JSON object of headers.
func fromJSON(s string) (cookie, authUser string, ok bool) {
	if !strings.HasPrefix(s, "{") {
		return "", "", false
	}
	var headers map[string]any
	if json.Unmarshal([]byte(s), &headers) != nil {
		return "", "", false
	}
	for name, v := range headers {
		switch {
		case strings.EqualFold(name, "cookie"):
			cookie, _ = v.(string)
		case strings.EqualFold(name, "x-goog-authuser"):
			switch v := v.(type) {
			case string:
				authUser = v
			case float64:
				authUser = strconv.Itoa(int(v))
			}
		}
	}
	return cookie, authUser, cookie != ""
}

// fromHeaders finds Cookie and X-Goog-AuthUser in a block of headers.
// Chrome may copy a name and its value on separate lines, with or without
// a colon after the name: pendingName holds the name until its value.
func fromHeaders(block string) (cookie, authUser string, ok bool) {
	var pendingName string
	for line := range strings.Lines(block) {
		line = curlHeader(strings.TrimSpace(line))
		if line == "" {
			continue
		}
		name, value, hasColon := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		switch {
		case pendingName != "" && (!hasColon || !validName(name)):
			name, value, pendingName = pendingName, line, ""
		case hasColon && strings.TrimSpace(value) == "" && validName(name):
			pendingName = name
			continue
		case !hasColon && validName(line):
			pendingName = line
			continue
		case !hasColon || name == "":
			// HTTP/2 pseudo-headers (":authority: …") and other lines.
			pendingName = ""
			continue
		default:
			pendingName = ""
		}
		value = strings.TrimSpace(value)
		// EqualFold rather than ToLower, which copies every Title-Case name.
		switch {
		case strings.EqualFold(name, "cookie"):
			cookie, ok = value, true
		case strings.EqualFold(name, "x-goog-authuser"):
			authUser = value
		}
	}
	return cookie, authUser, ok
}

// curlHeader turns a curl argument line ("-H 'cookie: …' \" or "-b '…'")
// into a header line; other lines come back unchanged.
func curlHeader(line string) string {
	line = strings.TrimSpace(strings.TrimSuffix(line, "\\"))
	for _, flag := range []string{"-H ", "--header "} {
		if rest, ok := strings.CutPrefix(line, flag); ok {
			return unquote(rest)
		}
	}
	for _, flag := range []string{"-b ", "--cookie "} {
		if rest, ok := strings.CutPrefix(line, flag); ok {
			return "cookie: " + unquote(rest)
		}
	}
	return line
}

// unquote strips one pair of shell quotes.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "$") // Chrome's Bash copy quotes as $'…'.
	if len(s) >= 2 && (s[0] == '\'' || s[0] == '"') && s[len(s)-1] == s[0] {
		s = s[1 : len(s)-1]
	}
	return s
}

// validName reports whether s is an HTTP token, as header and cookie names are.
func validName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r <= ' ' || r >= 0x7f || strings.ContainsRune(`"(),/:;<=>?@[\]{}`, r) {
			return false
		}
	}
	return true
}

// validValue rejects what would corrupt the Cookie header, like line
// breaks from a sloppy paste.
func validValue(s string) bool {
	for _, r := range s {
		if r < ' ' || r == 0x7f || r == ';' {
			return false
		}
	}
	return true
}

// IsZero reports whether c holds no credentials.
func (c Credentials) IsZero() bool { return c.sapisid == "" }

// String redacts the credentials so printing them by mistake leaks nothing.
func (c Credentials) String() string {
	if c.IsZero() {
		return "auth.Credentials{}"
	}
	return "auth.Credentials{redacted}"
}

// GoString redacts %#v too.
func (c Credentials) GoString() string { return c.String() }

// Secret encodes the credentials for a Store; Parse reads it back. Never log it.
func (c Credentials) Secret() string {
	if c.IsZero() {
		return ""
	}
	return "X-Goog-AuthUser: " + c.authUser + "\nCookie: " + c.cookie + "\n"
}

// Decorator returns a request signer for innertube's Auth. now stamps
// SAPISIDHASH; nil means time.Now.
func (c Credentials) Decorator(now func() time.Time) func(*http.Request) error {
	if now == nil {
		now = time.Now
	}
	// Built once per sign-in, not per request; read-only after, so
	// concurrent requests may share it.
	userNames := cookieNames(c.cookie)
	return func(r *http.Request) error {
		if c.IsZero() {
			return ErrInvalid
		}
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = defaultOrigin
		}
		r.Header.Set("Cookie", mergeCookies(c.cookie, userNames, r.Header.Get("Cookie")))
		r.Header.Set("Authorization", SAPISIDHash(c.sapisid, origin, now()))
		r.Header.Set("X-Origin", origin)
		r.Header.Set("X-Goog-AuthUser", c.authUser)
		return nil
	}
}

// SAPISIDHash is the Authorization header Google's web apps send:
// "SAPISIDHASH <ts>_<sha1(ts SAPISID origin)>".
func SAPISIDHash(sapisid, origin string, now time.Time) string {
	// Every request signs: with stack buffers only the returned string
	// allocates, where concatenating made 5 (BenchmarkSAPISIDHash).
	var tsBuf [20]byte
	ts := strconv.AppendInt(tsBuf[:0], now.Unix(), 10)
	in := make([]byte, 0, 256)
	in = append(append(append(append(append(in, ts...), ' '), sapisid...), ' '), origin...)
	// SHA-1 because Google defines the hash so; it proves the request
	// knows SAPISID, it protects nothing.
	sum := sha1.Sum(in) //nolint:gosec // see above
	out := make([]byte, 0, len("SAPISIDHASH ")+len(tsBuf)+len("_")+2*sha1.Size)
	out = append(append(append(out, "SAPISIDHASH "...), ts...), '_')
	return string(hex.AppendEncode(out, sum[:]))
}

// mergeCookies keeps the transport's cookies (like the consent cookie)
// unless the user's session has one of the same name. userNames is
// cookieNames(user).
func mergeCookies(user string, userNames map[string]bool, existing string) string {
	if existing == "" {
		return user
	}
	// Grown at the first cookie kept, so keeping none allocates nothing.
	var merged strings.Builder
	for part := range strings.SplitSeq(existing, ";") {
		part = strings.TrimSpace(part)
		name, _, _ := strings.Cut(part, "=")
		if part == "" || userNames[name] {
			continue
		}
		if merged.Len() == 0 {
			merged.Grow(len(user) + len("; ") + len(existing))
			merged.WriteString(user)
		}
		merged.WriteString("; ")
		merged.WriteString(part)
	}
	if merged.Len() == 0 {
		return user
	}
	return merged.String()
}

func cookieNames(cookie string) map[string]bool {
	names := map[string]bool{}
	for part := range strings.SplitSeq(cookie, ";") {
		name, _, _ := strings.Cut(strings.TrimSpace(part), "=")
		names[name] = true
	}
	return names
}
