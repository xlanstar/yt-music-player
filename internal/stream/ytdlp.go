package stream

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

const (
	defaultYTDLPTimeout = 30 * time.Second
	// ytdlpFormat is itag 251, else any Opus in WebM: what the engine plays.
	ytdlpFormat = "251/bestaudio[ext=webm][acodec=opus]"
)

// installDirs are where yt-dlp and the JS runtimes it needs are usually
// installed, which ytmd's PATH may lack: launchd gives
// /usr/bin:/bin:/usr/sbin:/sbin.
func installDirs() []string {
	if runtime.GOOS == "windows" {
		return nil
	}
	dirs := []string{"/opt/homebrew/bin", "/usr/local/bin"}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"), filepath.Join(home, ".deno", "bin"))
	}
	return append(dirs, "/opt/local/bin")
}

// searchPath is PATH, then the installDirs it lacks.
func searchPath() []string {
	dirs := filepath.SplitList(os.Getenv("PATH"))
	for _, dir := range installDirs() {
		if !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// lookPath finds the executable name in dirs as exec.LookPath does in
// PATH; a name with a directory is taken as is.
func lookPath(name string, dirs []string) (string, error) {
	if filepath.Base(name) != name {
		return name, nil
	}
	for _, dir := range dirs {
		// As exec.LookPath, never what the working directory holds.
		if !filepath.IsAbs(dir) {
			continue
		}
		if p, err := exec.LookPath(filepath.Join(dir, name)); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s: %w", name, exec.ErrNotFound)
}

type ytdlpInfo struct {
	URL            string            `json:"url"`
	Protocol       string            `json:"protocol"`
	Ext            string            `json:"ext"`
	ACodec         string            `json:"acodec"`
	ABR            float64           `json:"abr"`
	TBR            float64           `json:"tbr"`
	Filesize       int64             `json:"filesize"`
	FilesizeApprox int64             `json:"filesize_approx"`
	Duration       float64           `json:"duration"`
	HTTPHeaders    map[string]string `json:"http_headers"`
}

// viaYTDLP asks yt-dlp, which follows YouTube's changes within days and,
// with a JS runtime or PO token plugin installed, gets past what the
// clients here cannot. It keeps the user's yt-dlp config for that reason.
func (y *YouTube) viaYTDLP(ctx context.Context, videoID string) (Stream, error) {
	dirs := searchPath()
	bin, err := lookPath(y.opts.YTDLP, dirs)
	if err != nil {
		return Stream{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, y.opts.YTDLPTimeout)
	defer cancel()
	// "--" because IDs may start with "-"; Resolve has checked the ID.
	//nolint:gosec // the configured yt-dlp, on a checked ID
	cmd := exec.CommandContext(ctx, bin, "-f", ytdlpFormat, "-j", "--no-playlist", "--no-warnings",
		"--", "https://www.youtube.com/watch?v="+videoID)
	// On the same PATH, yt-dlp finds whichever JS runtime (Deno, Node, Bun,
	// QuickJS) is installed, as in a shell, rather than ytmd picking one.
	cmd.Env = append(os.Environ(), "PATH="+strings.Join(dirs, string(filepath.ListSeparator)))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// A child that ignores the kill must not hold Resolve past its timeout.
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return Stream{}, fmt.Errorf("yt-dlp: %w", ctx.Err())
		}
		if msg := lastError(stderr.String()); msg != "" {
			return Stream{}, fmt.Errorf("yt-dlp: %s", msg)
		}
		return Stream{}, fmt.Errorf("yt-dlp: %w", err)
	}
	s, err := parseYTDLP(stdout.Bytes(), y.now())
	if err != nil {
		return Stream{}, err
	}
	if err := y.probe(ctx, s); err != nil {
		return Stream{}, fmt.Errorf("yt-dlp: %w", err)
	}
	return s, nil
}

func parseYTDLP(data []byte, now time.Time) (Stream, error) {
	var info ytdlpInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return Stream{}, fmt.Errorf("yt-dlp: %w", err)
	}
	if info.URL == "" || (info.Protocol != "" && info.Protocol != "https") {
		return Stream{}, fmt.Errorf("yt-dlp: no direct URL (protocol %q)", info.Protocol)
	}
	if info.Ext != "webm" || info.ACodec != "opus" {
		return Stream{}, fmt.Errorf("yt-dlp: picked %s %q, not Opus in WebM", info.Ext, info.ACodec)
	}
	s := Stream{
		URL:      info.URL,
		MimeType: `audio/webm; codecs="opus"`,
		Bitrate:  int(math.Round(max(info.ABR, info.TBR) * 1000)),
		Size:     info.Filesize,
		Seconds:  info.Duration,
		Expires:  expiry(info.URL, 0, now),
	}
	if s.Size == 0 {
		s.Size = info.FilesizeApprox
	}
	if len(info.HTTPHeaders) > 0 {
		s.Header = make(http.Header, len(info.HTTPHeaders))
		for k, v := range info.HTTPHeaders {
			s.Header.Set(k, v)
		}
	}
	return s, nil
}

// lastError is yt-dlp's last "ERROR:" line, which says why, else its last
// line.
func lastError(stderr string) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	for _, line := range slices.Backward(lines) {
		if after, ok := strings.CutPrefix(strings.TrimSpace(line), "ERROR: "); ok {
			return after
		}
	}
	return strings.TrimSpace(lines[len(lines)-1])
}
