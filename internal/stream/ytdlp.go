package stream

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"math"
	"net/http"
	"os/exec"
	"slices"
	"strings"
	"time"
)

const (
	defaultYTDLPTimeout = 30 * time.Second
	// ytdlpFormat is itag 140, else any AAC; AVPlayer cannot play Opus.
	ytdlpFormat = "140/bestaudio[ext=m4a]"
)

// LookYTDLP returns the path of yt-dlp on PATH, or "" when there is none,
// for Options.YTDLP.
func LookYTDLP() string {
	p, err := exec.LookPath("yt-dlp")
	if err != nil {
		return ""
	}
	return p
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
	ctx, cancel := context.WithTimeout(ctx, y.opts.YTDLPTimeout)
	defer cancel()
	// "--" because IDs may start with "-"; Resolve has checked the ID.
	//nolint:gosec // the configured yt-dlp, on a checked ID
	cmd := exec.CommandContext(ctx, y.opts.YTDLP, "-f", ytdlpFormat, "-j", "--no-playlist", "--no-warnings",
		"--", "https://www.youtube.com/watch?v="+videoID)
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
	if info.Ext != "m4a" && info.Ext != "mp4" {
		return Stream{}, fmt.Errorf("yt-dlp: picked %q, not audio/mp4", info.Ext)
	}
	s := Stream{
		URL:      info.URL,
		MimeType: "audio/mp4",
		Bitrate:  int(math.Round(max(info.ABR, info.TBR) * 1000)),
		Size:     info.Filesize,
		Seconds:  info.Duration,
		Expires:  expiry(info.URL, 0, now),
	}
	if info.ACodec != "" && info.ACodec != "none" {
		s.MimeType += `; codecs="` + info.ACodec + `"`
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
