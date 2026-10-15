package auth

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// Sinks keep the compiler from optimizing measured calls away.
var (
	sinkCreds  Credentials
	sinkString string
	sinkErr    error
	sinkNames  map[string]bool
)

// benchCookie is a session's Cookie header at its real size (about 2 KB,
// 20 cookies), with made-up values.
var benchCookie = strings.Join([]string{
	"VISITOR_INFO1_LIVE=" + strings.Repeat("v", 11),
	"VISITOR_PRIVACY_METADATA=" + strings.Repeat("m", 24),
	"PREF=f6=40000000&tz=Europe.Paris&f7=100",
	"SID=g." + strings.Repeat("s", 150),
	"__Secure-1PSID=g." + strings.Repeat("a", 150),
	"__Secure-3PSID=g." + strings.Repeat("b", 150),
	"HSID=" + strings.Repeat("h", 17),
	"SSID=" + strings.Repeat("i", 17),
	"APISID=" + strings.Repeat("j", 34),
	"SAPISID=fakeSAPISID/abc" + strings.Repeat("k", 18),
	"__Secure-1PAPISID=fakeSAPISID/abc" + strings.Repeat("k", 18),
	"__Secure-3PAPISID=fakeSAPISID/abc" + strings.Repeat("k", 18),
	"LOGIN_INFO=" + strings.Repeat("l", 300),
	"SIDCC=" + strings.Repeat("c", 80),
	"__Secure-1PSIDCC=" + strings.Repeat("d", 80),
	"__Secure-3PSIDCC=" + strings.Repeat("e", 80),
	"__Secure-1PSIDTS=sidts-" + strings.Repeat("t", 80),
	"__Secure-3PSIDTS=sidts-" + strings.Repeat("u", 80),
	"YSC=" + strings.Repeat("y", 11),
	"SOCS=CAISEwgDEgk",
}, "; ")

// benchInputs are the shapes Parse accepts, as each browser copies them.
// Allocs today: most shapes 6 or 7, the jar's and the cookie's;
// browser.json 21, the JSON decoder's own.
var benchInputs = []struct {
	name  string
	limit float64
	in    string
}{
	{"value", 8, benchCookie},
	{"cookie_line", 8, "Cookie: " + benchCookie},
	{"chrome", 8, ":authority: music.youtube.com\n:method: POST\n:path: /youtubei/v1/browse?prettyPrint=false\n:scheme: https\n" +
		"accept: */*\naccept-encoding: gzip, deflate, br, zstd\naccept-language: en-GB,en;q=0.9\n" +
		"authorization: SAPISIDHASH 1700000000_0123456789abcdef0123456789abcdef01234567\ncontent-length: 2310\n" +
		"content-type: application/json\ncookie: " + benchCookie + "\norigin: https://music.youtube.com\npriority: u=1, i\n" +
		"referer: https://music.youtube.com/\nsec-ch-ua: \"Chromium\";v=\"131\", \"Not_A Brand\";v=\"24\"\nsec-fetch-mode: same-origin\n" +
		"user-agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36\n" +
		"x-goog-authuser: 1\nx-goog-visitor-id: CgtWSVNJVE9SXzEyMw%3D%3D\nx-origin: https://music.youtube.com\nx-youtube-client-name: 67\n" +
		"x-youtube-client-version: 1.20260101.01.00\n"},
	{"chrome_lines", 8, "accept\n*/*\naccept-language\nen-GB,en;q=0.9\ncontent-type\napplication/json\ncookie\n" + benchCookie +
		"\norigin\nhttps://music.youtube.com\nreferer\nhttps://music.youtube.com/\nx-goog-authuser\n1\nx-origin\nhttps://music.youtube.com\n"},
	{"firefox", 8, "POST /youtubei/v1/browse?prettyPrint=false HTTP/2\nHost: music.youtube.com\n" +
		"User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:133.0) Gecko/20100101 Firefox/133.0\nAccept: */*\n" +
		"Accept-Language: en-GB,en;q=0.5\nAccept-Encoding: gzip, deflate, br, zstd\nContent-Type: application/json\n" +
		"Content-Length: 2310\nReferer: https://music.youtube.com/\nX-Goog-Visitor-Id: CgtWSVNJVE9SXzEyMw%3D%3D\n" +
		"X-Youtube-Client-Name: 67\nX-Youtube-Client-Version: 1.20260101.01.00\n" +
		"Authorization: SAPISIDHASH 1700000000_0123456789abcdef0123456789abcdef01234567\nX-Goog-AuthUser: 1\n" +
		"X-Origin: https://music.youtube.com\nOrigin: https://music.youtube.com\nConnection: keep-alive\nCookie: " + benchCookie +
		"\nSec-Fetch-Dest: empty\nSec-Fetch-Mode: same-origin\nTE: trailers\n"},
	{"safari", 8, "Accept: */*\nAuthorization: SAPISIDHASH 1700000000_0123456789abcdef0123456789abcdef01234567\n" +
		"Content-Type: application/json\nOrigin: https://music.youtube.com\nReferer: https://music.youtube.com/\n" +
		"User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.2 Safari/605.1.15\n" +
		"Cookie: " + benchCookie + "\nX-Goog-AuthUser: 1\nX-Origin: https://music.youtube.com\nX-Youtube-Client-Name: 67\n" +
		"X-Youtube-Client-Version: 1.20260101.01.00\n"},
	{"curl", 8, "curl 'https://music.youtube.com/youtubei/v1/browse?prettyPrint=false' \\\n  -H 'accept: */*' \\\n" +
		"  -H 'accept-language: en-GB,en;q=0.9' \\\n  -H 'authorization: SAPISIDHASH 1700000000_0123456789abcdef0123456789abcdef01234567' \\\n" +
		"  -H 'content-type: application/json' \\\n  -b '" + benchCookie + "' \\\n  -H 'origin: https://music.youtube.com' \\\n" +
		"  -H 'x-goog-authuser: 1' \\\n  -H 'x-origin: https://music.youtube.com' \\\n" +
		"  --data-raw '{\"context\":{\"client\":{\"clientName\":\"WEB_REMIX\",\"clientVersion\":\"1.20260101.01.00\"}},\"browseId\":\"FEmusic_home\"}'"},
	{"browser_json", 25, `{"accept": "*/*", "accept-language": "en-GB,en;q=0.9", "content-type": "application/json", ` +
		`"cookie": "` + benchCookie + `", "origin": "https://music.youtube.com", "x-goog-authuser": "1", ` +
		`"x-origin": "https://music.youtube.com", "user-agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)"}`},
}

func benchCredentials(b *testing.B) Credentials {
	b.Helper()
	c, err := Parse("X-Goog-AuthUser: 1\nCookie: " + benchCookie)
	if err != nil {
		b.Fatal(err)
	}
	return c
}

func BenchmarkParse(b *testing.B) {
	for _, tc := range benchInputs {
		b.Run(tc.name, func(b *testing.B) {
			if _, err := Parse(tc.in); err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(tc.in)))
			b.ReportAllocs()
			for b.Loop() {
				sinkCreds, sinkErr = Parse(tc.in)
			}
		})
	}
}

// BenchmarkDecorator signs as innertube does on every attempt, over the
// transport's consent cookie.
func BenchmarkDecorator(b *testing.B) {
	sign := benchCredentials(b).Decorator(func() time.Time { return time.Unix(1_700_000_000, 0) })
	req, err := http.NewRequestWithContext(b.Context(), http.MethodPost, "https://music.youtube.com/youtubei/v1/browse", nil)
	if err != nil {
		b.Fatal(err)
	}
	consent := []string{"SOCS=CAI"}
	b.ReportAllocs()
	for b.Loop() {
		req.Header["Cookie"] = consent
		sinkErr = sign(req)
	}
}

func BenchmarkSAPISIDHash(b *testing.B) {
	now := time.Unix(1_700_000_000, 0)
	b.ReportAllocs()
	for b.Loop() {
		sinkString = SAPISIDHash("fakeSAPISID/abc", defaultOrigin, now)
	}
}

func BenchmarkMergeCookies(b *testing.B) {
	user := benchCredentials(b).cookie
	names := cookieNames(user)
	b.ReportAllocs()
	for b.Loop() {
		sinkString = mergeCookies(user, names, "SOCS=CAI; PREF=hl=en")
	}
}

func BenchmarkCookieNames(b *testing.B) {
	user := benchCredentials(b).cookie
	b.ReportAllocs()
	for b.Loop() {
		sinkNames = cookieNames(user)
	}
}

func BenchmarkSecret(b *testing.B) {
	c := benchCredentials(b)
	b.ReportAllocs()
	for b.Loop() {
		sinkString = c.Secret()
	}
}

func BenchmarkMemoryStore(b *testing.B) {
	c := benchCredentials(b)
	b.Run("load", func(b *testing.B) {
		s := &MemoryStore{}
		_ = s.Save(c)
		b.ReportAllocs()
		for b.Loop() {
			sinkCreds, sinkErr = s.Load()
		}
	})
	b.Run("save", func(b *testing.B) {
		s := &MemoryStore{}
		b.ReportAllocs()
		for b.Loop() {
			sinkErr = s.Save(c)
		}
	})
}
