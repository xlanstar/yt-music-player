package stream

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// maxPlayerResponse bounds a player response; real ones are under 200 KB.
const maxPlayerResponse = 8 << 20

// PlayabilityError is a player response whose playabilityStatus is not
// OK: the song is removed, private, region-locked, age-gated, or needs a
// sign-in.
type PlayabilityError struct {
	Client string // e.g. "VISIONOS"
	Status string // e.g. "LOGIN_REQUIRED", "UNPLAYABLE", "ERROR"
	Reason string // InnerTube's message, in English
}

func (e *PlayabilityError) Error() string {
	// No "stream:" prefix: Resolve adds it with the video ID.
	msg := "unplayable: " + e.Client + ": " + e.Status
	if e.Reason != "" {
		msg += ": " + e.Reason
	}
	return msg
}

// Unwrap makes errors.Is(err, ErrUnplayable) hold.
func (e *PlayabilityError) Unwrap() error { return ErrUnplayable }

// isBotCheck reports whether InnerTube distrusts the session rather than
// the song; a fresh visitorData usually clears it.
func (e *PlayabilityError) isBotCheck() bool {
	return e.Status == "LOGIN_REQUIRED" && strings.Contains(strings.ToLower(e.Reason), "not a bot")
}

// isFinal reports whether the song is gone, private or age-gated: what
// yt-dlp, run without cookies, cannot get past either. Bot checks, PO
// tokens and UNPLAYABLE (also "made for kids") are left to it.
func (e *PlayabilityError) isFinal() bool {
	reason := strings.ToLower(e.Reason)
	switch e.Status {
	case "ERROR":
		return strings.Contains(reason, "unavailable")
	case "LOGIN_REQUIRED":
		return strings.Contains(reason, "confirm your age") || strings.Contains(reason, "private")
	}
	return false
}

type playerResponse struct {
	ResponseContext struct {
		VisitorData string `json:"visitorData"`
	} `json:"responseContext"`
	PlayabilityStatus struct {
		Status      string `json:"status"`
		Reason      string `json:"reason"`
		ErrorScreen struct {
			PlayerErrorMessageRenderer struct {
				Subreason text `json:"subreason"`
			} `json:"playerErrorMessageRenderer"`
		} `json:"errorScreen"`
	} `json:"playabilityStatus"`
	StreamingData struct {
		ExpiresInSeconds string   `json:"expiresInSeconds"`
		AdaptiveFormats  []format `json:"adaptiveFormats"`
	} `json:"streamingData"`
	VideoDetails struct {
		VideoID       string `json:"videoId"`
		LengthSeconds string `json:"lengthSeconds"`
	} `json:"videoDetails"`
}

type text struct {
	SimpleText string `json:"simpleText"`
	Runs       []struct {
		Text string `json:"text"`
	} `json:"runs"`
}

func (t text) String() string {
	if t.SimpleText != "" {
		return t.SimpleText
	}
	var b strings.Builder
	for _, r := range t.Runs {
		b.WriteString(r.Text)
	}
	return b.String()
}

type format struct {
	Itag             int    `json:"itag"`
	URL              string `json:"url"`
	SignatureCipher  string `json:"signatureCipher"`
	MimeType         string `json:"mimeType"`
	Bitrate          int    `json:"bitrate"`
	AverageBitrate   int    `json:"averageBitrate"`
	ContentLength    string `json:"contentLength"`
	ApproxDurationMs string `json:"approxDurationMs"`
	IsDrc            bool   `json:"isDrc"`
	DrmFamilies      []any  `json:"drmFamilies"`
	Type             string `json:"type"`
	// TargetDurationSec is only set on live segments.
	TargetDurationSec float64 `json:"targetDurationSec"`
	AudioTrack        *struct {
		AudioIsDefault bool `json:"audioIsDefault"`
	} `json:"audioTrack"`
}

func (y *YouTube) player(ctx context.Context, c client, videoID, visitorData string) (*playerResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	body, err := playerBody(c, videoID, visitorData)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		y.opts.BaseURL+"player?prettyPrint=false",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, err
	}
	header := req.Header
	header.Set("Content-Type", "application/json")
	header.Set("Origin", origin)
	header.Set("X-YouTube-Client-Name", strconv.Itoa(c.ID))
	header.Set("X-YouTube-Client-Version", c.Version)
	if c.UserAgent != "" {
		header.Set("User-Agent", c.UserAgent)
	}
	if visitorData != "" {
		header.Set("X-Goog-Visitor-Id", visitorData)
	}
	resp, err := y.opts.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	// Read in full, so a Close error loses nothing.
	defer func() { _ = resp.Body.Close() }()
	respBody := io.LimitReader(resp.Body, maxPlayerResponse)
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, respBody)
		return nil, fmt.Errorf("player: HTTP %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	pr, err := parsePlayer(respBody)
	if err != nil {
		return nil, fmt.Errorf("player: %w", err)
	}
	return pr, nil
}

// playerRequest is the player endpoint's body. Structs, not maps, which
// allocate per key and sort: every attempt of every song sends one
// (BenchmarkPlayerBody).
type playerRequest struct {
	Context clientContext `json:"context"`
	VideoID string        `json:"videoId"`
	// As yt-dlp sends: without them some songs answer CONTENT_CHECK_REQUIRED.
	PlaybackContext struct {
		ContentPlaybackContext struct {
			HTML5Preference string `json:"html5Preference"`
		} `json:"contentPlaybackContext"`
	} `json:"playbackContext"`
	ContentCheckOK bool `json:"contentCheckOk"`
	RacyCheckOK    bool `json:"racyCheckOk"`
}

func playerBody(c client, videoID, visitorData string) ([]byte, error) {
	req := playerRequest{
		Context:        c.context(visitorData),
		VideoID:        videoID,
		ContentCheckOK: true,
		RacyCheckOK:    true,
	}
	req.PlaybackContext.ContentPlaybackContext.HTML5Preference = "HTML5_PREF_WANTS"
	return json.Marshal(&req)
}

// parsePlayer decodes as it reads: io.ReadAll's growing copies were 4/5
// of a real response's allocated bytes (BenchmarkParsePlayer).
func parsePlayer(r io.Reader) (*playerResponse, error) {
	var pr playerResponse
	if err := json.UnmarshalRead(r, &pr); err != nil {
		return nil, err
	}
	return &pr, nil
}

// playability returns nil for an OK response.
func (pr *playerResponse) playability(clientName string) *PlayabilityError {
	ps := pr.PlayabilityStatus
	if ps.Status == "OK" {
		return nil
	}
	reason := ps.Reason
	if sub := ps.ErrorScreen.PlayerErrorMessageRenderer.Subreason.String(); sub != "" && sub != reason {
		reason = strings.TrimPrefix(reason+": "+sub, ": ")
	}
	status := ps.Status
	if status == "" {
		status = "NO_STATUS"
	}
	return &PlayabilityError{Client: clientName, Status: status, Reason: reason}
}

// unplayable wraps ErrUnplayable for reasons InnerTube did not give,
// worded to follow Resolve's "stream: <id>:" prefix.
type unplayable string

func (e unplayable) Error() string { return "unplayable: " + string(e) }
func (unplayable) Unwrap() error   { return ErrUnplayable }

// errNoFormat is a playable song without a format the engine can fetch: its
// audio is only behind a signature or n challenge, or SABR.
const errNoFormat = unplayable("no direct Opus format")

// stream picks the best Opus format of an OK response.
func (pr *playerResponse) stream(c client, now time.Time) (Stream, error) {
	f, ok := pickFormat(pr.StreamingData.AdaptiveFormats)
	if !ok {
		return Stream{}, fmt.Errorf("%s: %w", c.Name, errNoFormat)
	}
	s := Stream{
		URL:      f.URL,
		MimeType: f.MimeType,
		Bitrate:  f.AverageBitrate,
		Size:     atoi64(f.ContentLength),
		Seconds:  float64(atoi64(f.ApproxDurationMs)) / 1000,
		Expires:  expiry(f.URL, atoi64(pr.StreamingData.ExpiresInSeconds), now),
	}
	if s.Bitrate == 0 {
		s.Bitrate = f.Bitrate
	}
	if s.Seconds == 0 {
		s.Seconds = float64(atoi64(pr.VideoDetails.LengthSeconds))
	}
	// googlevideo ignores it today, but the URL was issued to this agent.
	if c.UserAgent != "" {
		s.Header = http.Header{"User-Agent": {c.UserAgent}}
	}
	return s, nil
}

// pickFormat prefers, in order: the default audio track (others are
// dubs), no dynamic range compression, then the highest bitrate: itag 251
// (Opus about 130k), or 774 (256k) where YouTube Music gives it.
func pickFormat(formats []format) (format, bool) {
	var best format
	var bestRank [3]int
	found := false
	for _, f := range formats {
		if !usable(f) {
			continue
		}
		rank := [3]int{0, 0, max(f.AverageBitrate, f.Bitrate)}
		if f.AudioTrack == nil || f.AudioTrack.AudioIsDefault {
			rank[0] = 1
		}
		if !f.IsDrc {
			rank[1] = 1
		}
		if !found || slices.Compare(rank[:], bestRank[:]) > 0 {
			best, bestRank, found = f, rank, true
		}
	}
	return best, found
}

// usable reports whether f is Opus in WebM that plays from its URL alone.
func usable(f format) bool {
	isDirectOpus := f.URL != "" && strings.HasPrefix(f.MimeType, "audio/webm") && strings.Contains(f.MimeType, "opus")
	isProtected := len(f.DrmFamilies) > 0
	isSegmented := f.TargetDurationSec > 0 || f.Type == "FORMAT_STREAM_TYPE_OTF"
	if !isDirectOpus || isProtected || isSegmented {
		return false
	}
	u, err := url.Parse(f.URL)
	if err != nil || u.Scheme != "https" {
		return false
	}
	// An n challenge left unsolved throttles googlevideo to a few dozen
	// KB/s; solving it needs the JS player.
	_, hasN := queryValue(u.RawQuery, "n")
	return !hasN
}

// expiry is the URL's expire parameter, else expiresIn seconds from now,
// else zero.
func expiry(rawURL string, expiresIn int64, now time.Time) time.Time {
	if u, err := url.Parse(rawURL); err == nil {
		expire, _ := queryValue(u.RawQuery, "expire")
		if sec := atoi64(expire); sec > 0 {
			return time.Unix(sec, 0)
		}
	}
	if expiresIn > 0 {
		return now.Add(time.Duration(expiresIn) * time.Second)
	}
	return time.Time{}
}

// queryValue is url.ParseQuery(rawQuery)'s first value of key, and
// whether it has one, without allocating url.Values for googlevideo's two
// dozen parameters: usable reads every format's (BenchmarkPickFormat).
func queryValue(rawQuery, key string) (string, bool) {
	for rawQuery != "" {
		var pair string
		pair, rawQuery, _ = strings.Cut(rawQuery, "&")
		if pair == "" || strings.Contains(pair, ";") {
			continue
		}
		k, v, _ := strings.Cut(pair, "=")
		if strings.ContainsAny(k, "%+") {
			if uk, err := url.QueryUnescape(k); err != nil || uk != key {
				continue
			}
		} else if k != key {
			continue
		}
		if !strings.ContainsAny(v, "%+") {
			return v, true
		}
		if uv, err := url.QueryUnescape(v); err == nil {
			return uv, true
		}
	}
	return "", false
}

func atoi64(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
