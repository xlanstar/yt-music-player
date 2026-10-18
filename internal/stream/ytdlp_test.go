package stream

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
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

// yt-dlp runs without cookies, so it is skipped only for songs gone,
// private or age-gated.
func TestYTDLPSkipped(t *testing.T) {
	for _, tt := range []struct {
		name    string
		answer  func(playerReq) string
		media   int
		wantRun bool
	}{
		{"unavailable", fixed("player_error.json"), 206, false},
		{"age-gated", fixed("player_login.json"), 206, false},
		{"bot check", fixed("player_bot.json"), 206, true},
		{"unplayable", fixed("player_unplayable.json"), 206, true},
		{"no direct format", fixed("player_cipher.json"), 206, true},
		{"po token", fixed("player_ok.json"), 403, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			bin, argsFile := fakeYTDLP(t, "exit 1")
			f := newFake(t, tt.answer)
			f.media = func(int) int { return tt.media }
			if _, err := f.resolver(Options{YTDLP: bin}).Resolve(t.Context(), okID); err == nil {
				t.Fatal("want an error")
			}
			_, err := os.Stat(argsFile)
			if ran := err == nil; ran != tt.wantRun {
				t.Errorf("yt-dlp ran = %v, want %v", ran, tt.wantRun)
			}
		})
	}
}

func TestIsFinal(t *testing.T) {
	for _, tt := range []struct {
		status, reason string
		want           bool
	}{
		{"ERROR", "This video is unavailable", true},
		{"ERROR", "Something went wrong", false},
		{"LOGIN_REQUIRED", "Sign in to confirm your age", true},
		{"LOGIN_REQUIRED", "This video is private", true},
		{"LOGIN_REQUIRED", "Sign in to confirm you’re not a bot", false},
		{"UNPLAYABLE", "Video unavailable", false},
		{"CONTENT_CHECK_REQUIRED", "", false},
	} {
		pe := &PlayabilityError{Client: "VISIONOS", Status: tt.status, Reason: tt.reason}
		if got := pe.isFinal(); got != tt.want {
			t.Errorf("%s %q: isFinal = %v, want %v", tt.status, tt.reason, got, tt.want)
		}
	}
}

// launchd starts ytmd on /usr/bin:/bin:/usr/sbin:/sbin, without where
// Homebrew, pipx or Deno's installer put yt-dlp and the JS runtimes it runs.
func TestSearchPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH alone on Windows")
	}
	t.Setenv("HOME", "/home/u")
	t.Setenv("PATH", "/usr/bin:/usr/local/bin")
	want := []string{"/usr/bin", "/usr/local/bin", "/opt/homebrew/bin", "/home/u/.local/bin", "/home/u/.deno/bin", "/opt/local/bin"}
	if got := searchPath(); !slices.Equal(got, want) {
		t.Errorf("searchPath = %q, want %q", got, want)
	}
}

func TestLookPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executables need a mode bit")
	}
	const name = "stream-test-yt-dlp"
	tmp := t.TempDir()
	exe := func(dir string) string {
		t.Helper()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		return p
	}
	onPath, home := filepath.Join(tmp, "path"), filepath.Join(tmp, "home")
	inHome := exe(filepath.Join(home, ".local", "bin"))
	t.Setenv("HOME", home)
	t.Chdir(tmp)
	exe(filepath.Join(tmp, "rel"))
	// Not executable: passed over.
	if err := os.WriteFile(filepath.Join(tmp, "plain"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "rel"+string(filepath.ListSeparator)+onPath)

	if got, err := lookPath(name, searchPath()); got != inHome || err != nil {
		t.Errorf("lookPath = %q, %v; want %q, past a relative PATH entry", got, err, inHome)
	}
	first := exe(onPath)
	if got, err := lookPath(name, searchPath()); got != first || err != nil {
		t.Errorf("lookPath = %q, %v; want PATH's %q first", got, err, first)
	}
	if got, err := lookPath("/x/yt-dlp", nil); got != "/x/yt-dlp" || err != nil {
		t.Errorf("lookPath(path) = %q, %v; want it as is", got, err)
	}
	if _, err := lookPath("plain", []string{tmp}); !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("err = %v, want exec.ErrNotFound", err)
	}
}

// yt-dlp is looked up by name when needed, and runs with the install
// directories on its PATH, where its JS runtime may be.
func TestYTDLPEnvironment(t *testing.T) {
	bin, _ := fakeYTDLP(t, "printf '%s' \"$PATH\" > \"${0%/*}/path\"\nexit 1")
	dir := filepath.Dir(bin)
	t.Setenv("HOME", "/home/u")
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	f := newFake(t, fixed("player_unplayable.json"))
	if _, err := f.resolver(Options{YTDLP: "yt-dlp"}).Resolve(t.Context(), okID); err == nil {
		t.Fatal("want an error")
	}
	got, err := os.ReadFile(filepath.Join(dir, "path"))
	if err != nil {
		t.Fatalf("yt-dlp did not run: %v", err)
	}
	want := dir + ":/usr/bin:/bin:/opt/homebrew/bin:/usr/local/bin:/home/u/.local/bin:/home/u/.deno/bin:/opt/local/bin"
	if string(got) != want {
		t.Errorf("yt-dlp's PATH = %q, want %q", got, want)
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
