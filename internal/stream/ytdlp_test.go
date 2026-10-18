package stream

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeYTDLP writes a shell script standing in for yt-dlp, which saves its
// arguments to argsFile.
func fakeYTDLP(t *testing.T, script string) (path, argsFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	dir := t.TempDir()
	path = filepath.Join(dir, "yt-dlp")
	argsFile = filepath.Join(dir, "args")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argsFile + "'\n" + script + "\n"
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path, argsFile
}

func TestYTDLPFallback(t *testing.T) {
	fixture, err := filepath.Abs(filepath.Join("testdata", "ytdlp.json"))
	if err != nil {
		t.Fatal(err)
	}
	bin, argsFile := fakeYTDLP(t, "cat '"+fixture+"'")
	f := newFake(t, fixed("player_unplayable.json"))
	y := f.resolver(Options{YTDLP: bin, NoProbe: true})
	id := "-FGBhQbmPw8" // IDs may start with a dash
	s, err := y.Resolve(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.players()) != 1 {
		t.Errorf("player requests = %d, want InnerTube asked first", len(f.players()))
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	want := "-f\n251/bestaudio[ext=webm][acodec=opus]\n-j\n--no-playlist\n--no-warnings\n--\n" +
		"https://www.youtube.com/watch?v=" + id + "\n"
	if string(args) != want {
		t.Errorf("args = %q, want %q", args, want)
	}
	if !strings.Contains(s.URL, "itag=251&") || s.MimeType != `audio/webm; codecs="opus"` ||
		s.Size != 4734201 || s.Seconds != 282 || s.Bitrate != 135893 {
		t.Errorf("stream = %+v", s)
	}
	if !s.Expires.Equal(time.Unix(1791988385, 0)) {
		t.Errorf("Expires = %v", s.Expires)
	}
	if ua := s.Header.Get("User-Agent"); !strings.Contains(ua, "Chrome/") || s.Header.Get("Sec-Fetch-Mode") != "navigate" {
		t.Errorf("Header = %v, want yt-dlp's http_headers", s.Header)
	}
}

func TestYTDLPFailure(t *testing.T) {
	bin, _ := fakeYTDLP(t, "echo '[youtube] x: Downloading' >&2\n"+
		"echo 'ERROR: [youtube] aaaaaaaaaaa: Video unavailable' >&2\n"+
		"exit 1")
	f := newFake(t, fixed("player_unplayable.json"))
	_, err := f.resolver(Options{YTDLP: bin, NoProbe: true}).Resolve(t.Context(), "aaaaaaaaaaa")
	if !errors.Is(err, ErrUnplayable) {
		t.Errorf("err = %v, want the client's ErrUnplayable kept", err)
	}
	if err == nil || !strings.Contains(err.Error(), "yt-dlp: [youtube] aaaaaaaaaaa: Video unavailable") {
		t.Errorf("err = %v, want yt-dlp's ERROR line", err)
	}
}

func TestYTDLPTimeout(t *testing.T) {
	bin, _ := fakeYTDLP(t, "exec sleep 30")
	f := newFake(t, fixed("player_unplayable.json"))
	y := f.resolver(Options{YTDLP: bin, YTDLPTimeout: 100 * time.Millisecond, NoProbe: true})
	start := time.Now()
	_, err := y.Resolve(t.Context(), okID)
	if !errors.Is(err, ErrUnplayable) || !strings.Contains(err.Error(), "yt-dlp: context deadline exceeded") {
		t.Errorf("err = %v, want yt-dlp timed out", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v", d)
	}
}

func TestParseYTDLP(t *testing.T) {
	for _, tt := range []struct{ name, json, want string }{
		{"hls", `{"url":"https://x/y.m3u8","protocol":"m3u8_native","ext":"mp4"}`, "no direct URL"},
		{"aac", `{"url":"https://x/y","protocol":"https","ext":"m4a","acodec":"mp4a.40.2"}`, "not Opus in WebM"},
		{"garbage", `ERROR`, "yt-dlp: jsontext: invalid character"},
	} {
		if _, err := parseYTDLP([]byte(tt.json), time.Now()); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want %q", tt.name, err, tt.want)
		}
	}
}
