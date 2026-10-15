package auth

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// fakeCookie is shaped like a real session's, with made-up values.
const fakeCookie = "VISITOR_INFO1_LIVE=fakevisitor; SID=fakesid; __Secure-3PSID=fake3psid; " +
	"SAPISID=fakeSAPISID/abc; __Secure-3PAPISID=fakeSAPISID/abc; SOCS=CAISEwgDEgk"

func TestParseForms(t *testing.T) {
	for _, tc := range []struct {
		name, in, authUser string
	}{
		{"value", fakeCookie, "0"},
		{"value with spaces", "  " + fakeCookie + " \n", "0"},
		{"quoted value", `"` + fakeCookie + `"`, "0"},
		{"cookie line", "Cookie: " + fakeCookie, "0"},
		{"lowercase line", "cookie:" + fakeCookie, "0"},
		{"firefox block", "POST /youtubei/v1/browse?prettyPrint=false HTTP/2\nHost: music.youtube.com\n" +
			"User-Agent: Mozilla/5.0\nAccept: */*\nX-Goog-AuthUser: 2\nCookie: " + fakeCookie + "\nTE: trailers\n", "2"},
		{"chrome block", ":authority: music.youtube.com\n:method: POST\n:path: /youtubei/v1/browse\n" +
			"accept: */*\ncookie: " + fakeCookie + "\r\nx-goog-authuser: 1\r\nx-origin: https://music.youtube.com\n", "1"},
		{"chrome separate lines", "accept\n*/*\ncookie\n" + fakeCookie + "\nx-goog-authuser\n3\norigin\nhttps://music.youtube.com\n", "3"},
		{"chrome separate lines with colons", "accept:\n*/*\ncookie:\n" + fakeCookie + "\nx-goog-authuser:\n1\n", "1"},
		{"curl", "curl 'https://music.youtube.com/youtubei/v1/browse?prettyPrint=false' \\\n  -H 'accept: */*' \\\n" +
			"  -b '" + fakeCookie + "' \\\n  -H 'x-goog-authuser: 4' \\\n  --data-raw '{\"context\":{}}'", "4"},
		{"curl header", "curl 'https://music.youtube.com/' \\\n  -H 'Cookie: " + fakeCookie + "' \\\n  -H 'X-Goog-AuthUser: 1'", "1"},
		{"ytmusicapi json", `{"cookie": "` + fakeCookie + `", "x-goog-authuser": "1", "accept": "*/*"}`, "1"},
		{"bad authuser", "X-Goog-AuthUser: me\nCookie: " + fakeCookie, "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Parse(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if c.sapisid != "fakeSAPISID/abc" || c.authUser != tc.authUser {
				t.Errorf("sapisid %q, authuser %q, want %q", c.sapisid, c.authUser, tc.authUser)
			}
			if c.cookie != fakeCookie {
				t.Errorf("cookie = %q", c.cookie)
			}
		})
	}
}

func TestParseEitherSAPISID(t *testing.T) {
	for _, in := range []string{"SID=x; SAPISID=one", "SID=x; __Secure-3PAPISID=one"} {
		c, err := Parse(in)
		if err != nil || c.sapisid != "one" {
			t.Errorf("Parse(%q) = sapisid %q, %v", in, c.sapisid, err)
		}
	}
}

func TestParseDropsJunk(t *testing.T) {
	c, err := Parse("SAPISID=s; broken; =novalue; a b=c; SID=1; SID=2")
	if err != nil {
		t.Fatal(err)
	}
	if c.cookie != "SAPISID=s; SID=1" {
		t.Errorf("cookie = %q", c.cookie)
	}
}

func TestParseRejects(t *testing.T) {
	for _, in := range []string{
		"", "   ", "hello", "SID=fake; HSID=fake", "SAPISID=", "Cookie: SID=x",
		"Host: music.youtube.com\nAccept: */*\n", `{"cookie": "SID=x"}`,
	} {
		_, err := Parse(in)
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("Parse(%q): err = %v, want ErrInvalid", in, err)
		}
	}
}

func TestErrorsLeakNothing(t *testing.T) {
	secret := "SID=topsecretvalue; HSID=topsecretvalue"
	_, err := Parse(secret)
	if err == nil || strings.Contains(err.Error(), "topsecret") {
		t.Errorf("err = %v", err)
	}
}

func TestRedacted(t *testing.T) {
	c, err := Parse(fakeCookie)
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		if s := fmt.Sprintf(format, c); strings.Contains(s, "fake") {
			t.Errorf("%s printed the secret: %s", format, s)
		}
	}
	if s := fmt.Sprintf("%v", Credentials{}); s != "auth.Credentials{}" {
		t.Errorf("zero = %s", s)
	}
}

func TestSecretRoundTrip(t *testing.T) {
	c, err := Parse("X-Goog-AuthUser: 2\nCookie: " + fakeCookie)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(c.Secret())
	if err != nil || back != c {
		t.Errorf("round trip = %v (%v), equal %v", back, err, back == c)
	}
	if (Credentials{}).Secret() != "" {
		t.Error("zero has a secret")
	}
}

func TestDecorator(t *testing.T) {
	c, err := Parse("X-Goog-AuthUser: 1\nCookie: " + fakeCookie)
	if err != nil {
		t.Fatal(err)
	}
	clock := func() time.Time { return time.Unix(1_700_000_000, 0) }
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://music.youtube.com/youtubei/v1/browse", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "https://music.youtube.com")
	req.Header.Set("Cookie", "SOCS=CAI; PREF=hl=en")
	if err := c.Decorator(clock)(req); err != nil {
		t.Fatal(err)
	}
	// sha1("1700000000 fakeSAPISID/abc https://music.youtube.com"), computed apart.
	want := map[string]string{
		"Authorization":   "SAPISIDHASH 1700000000_e3fb513d320124d12054787545ecfa2502fd6de2",
		"X-Origin":        "https://music.youtube.com",
		"X-Goog-AuthUser": "1",
		// The session's SOCS wins over the transport's; PREF is kept.
		"Cookie": fakeCookie + "; PREF=hl=en",
	}
	for k, v := range want {
		if got := req.Header.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestDecoratorDefaultOrigin(t *testing.T) {
	c, _ := Parse(fakeCookie)
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://example.com/", nil)
	if err := c.Decorator(func() time.Time { return time.Unix(1_700_000_000, 0) })(req); err != nil {
		t.Fatal(err)
	}
	hasHash := strings.HasSuffix(req.Header.Get("Authorization"), "_e3fb513d320124d12054787545ecfa2502fd6de2")
	if req.Header.Get("X-Origin") != defaultOrigin || !hasHash {
		t.Errorf("headers = %v", req.Header)
	}
	if req.Header.Get("Cookie") != fakeCookie {
		t.Errorf("Cookie = %q", req.Header.Get("Cookie"))
	}
}

func TestZeroDecoratorFails(t *testing.T) {
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://example.com/", nil)
	if err := (Credentials{}).Decorator(nil)(req); !errors.Is(err, ErrInvalid) {
		t.Errorf("err = %v", err)
	}
}
