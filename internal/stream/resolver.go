package stream

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

const (
	// DefaultBaseURL is InnerTube's API root, where the player endpoint lives.
	DefaultBaseURL = "https://www.youtube.com/youtubei/v1/"
	origin         = "https://www.youtube.com"

	// timeout bounds each player request and probe.
	timeout = 15 * time.Second
	// reuseMargin is how long a cached URL must still live to be reused:
	// googlevideo is read lazily while the song plays, so the URL must
	// outlast the song and some pausing. URLs live about 6h.
	reuseMargin = time.Hour
	// probeBytes are read from the end of the file: googlevideo serves the
	// first MiB of a URL lacking a required PO token, then answers 403.
	probeBytes = 1024
	// maxSessions is how many visitor sessions one client gets per song.
	maxSessions = 3
)

// errRefused is googlevideo refusing a URL the player endpoint issued.
var errRefused = errors.New("googlevideo refused the URL")

// Options configures New. The zero value is ready to use.
type Options struct {
	// HTTP sends player requests and probes; nil means http.DefaultClient.
	HTTP *http.Client
	// BaseURL is DefaultBaseURL unless set, e.g. to an httptest server.
	BaseURL string
	// Clients are tried in order; empty means VisionOS, then AndroidVR.
	Clients []Client
	// VisitorData seeds the visitor session. Without one, the first
	// player request is usually bot-checked; the resolver then adopts the
	// visitorData that answer carries.
	VisitorData string
	// NoProbe skips the range request proving googlevideo serves a URL
	// past its first MiB.
	NoProbe bool
	// YTDLP is the path of a yt-dlp binary, run after every client
	// failed; empty disables it. See LookYTDLP.
	YTDLP string
	// YTDLPTimeout bounds one yt-dlp run; zero means 30s.
	YTDLPTimeout time.Duration
}

// YouTube resolves songs over InnerTube's player endpoint. It caches
// streams until shortly before they expire and shares one resolve among
// concurrent callers of a song. It is safe for concurrent use.
type YouTube struct {
	opts Options
	now  func() time.Time

	mu      sync.Mutex
	visitor string
	cache   map[string]Stream
	calls   map[string]*call
}

var _ Resolver = (*YouTube)(nil)

// call is one resolve in flight, shared by every caller of its song.
type call struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	s       Stream
	err     error
}

// New returns a resolver configured by opts.
func New(opts Options) *YouTube {
	if opts.HTTP == nil {
		opts.HTTP = http.DefaultClient
	}
	opts.BaseURL = strings.TrimSuffix(cmp.Or(opts.BaseURL, DefaultBaseURL), "/") + "/"
	if len(opts.Clients) == 0 {
		opts.Clients = []Client{VisionOS, AndroidVR}
	}
	if opts.YTDLPTimeout <= 0 {
		opts.YTDLPTimeout = defaultYTDLPTimeout
	}
	return &YouTube{
		opts:    opts,
		now:     time.Now,
		visitor: opts.VisitorData,
		cache:   map[string]Stream{},
		calls:   map[string]*call{},
	}
}

// fresh reports whether s lives long enough to be handed out.
func (y *YouTube) fresh(s Stream) bool { return y.now().Before(s.Expires.Add(-reuseMargin)) }

// videoIDPattern also keeps IDs from reaching yt-dlp's argv as options.
var videoIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// Resolve returns the song's Opus stream. Errors wrap ErrUnplayable
// (and a *PlayabilityError, when InnerTube said why) for songs it cannot
// play; other errors, like being offline, may pass on retry.
func (y *YouTube) Resolve(ctx context.Context, videoID string) (Stream, error) {
	if !videoIDPattern.MatchString(videoID) {
		return Stream{}, fmt.Errorf("%w: invalid video id %q", ErrUnplayable, videoID)
	}
	if err := ctx.Err(); err != nil {
		return Stream{}, fmt.Errorf("stream: %s: %w", videoID, err)
	}

	y.mu.Lock()
	if s, ok := y.cache[videoID]; ok {
		if y.fresh(s) {
			y.mu.Unlock()
			return s.clone(), nil
		}
		delete(y.cache, videoID)
	}
	c := y.calls[videoID]
	if c == nil {
		// The resolve outlives a caller that gives up while others wait;
		// the last one to leave cancels it.
		resolveCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		c = &call{done: make(chan struct{}), cancel: cancel}
		y.calls[videoID] = c
		go y.run(resolveCtx, videoID, c)
	}
	c.waiters++
	y.mu.Unlock()

	select {
	case <-c.done:
		return c.s.clone(), c.err
	case <-ctx.Done():
		y.mu.Lock()
		c.waiters--
		if c.waiters == 0 && y.calls[videoID] == c {
			c.cancel()
			// A caller arriving now starts afresh instead of joining a
			// canceled resolve.
			delete(y.calls, videoID)
		}
		y.mu.Unlock()
		return Stream{}, fmt.Errorf("stream: %s: %w", videoID, ctx.Err())
	}
}

// Forget drops the song's cached stream, for when its URL stopped working
// early, e.g. after the network (and so the IP the URL is bound to)
// changed. It also ends the visitor session, in case YouTube started
// enforcing PO tokens on it.
func (y *YouTube) Forget(videoID string) {
	y.mu.Lock()
	delete(y.cache, videoID)
	y.visitor = ""
	y.mu.Unlock()
}

func (y *YouTube) run(ctx context.Context, videoID string, c *call) {
	defer c.cancel()
	s, err := y.resolve(ctx, videoID)
	y.mu.Lock()
	if err == nil && y.fresh(s) {
		maps.DeleteFunc(y.cache, func(_ string, cached Stream) bool { return !y.fresh(cached) })
		y.cache[videoID] = s
	}
	if y.calls[videoID] == c {
		delete(y.calls, videoID)
	}
	c.s, c.err = s, err
	close(c.done)
	y.mu.Unlock()
}

// resolve tries each client, then yt-dlp, keeping every failure so the
// error says why each was passed over.
func (y *YouTube) resolve(ctx context.Context, videoID string) (Stream, error) {
	var errs []error
	for _, c := range y.opts.Clients {
		s, err := y.viaClient(ctx, c, videoID)
		if err == nil {
			return s, nil
		}
		if ctx.Err() != nil {
			return Stream{}, fmt.Errorf("stream: %s: %w", videoID, ctx.Err())
		}
		log.Debug().Str("id", videoID).Str("client", c.Name).Err(err).Msg("stream client failed")
		errs = append(errs, err)
	}
	if y.opts.YTDLP != "" {
		s, err := y.viaYTDLP(ctx, videoID)
		if err == nil {
			return s, nil
		}
		if ctx.Err() != nil {
			return Stream{}, fmt.Errorf("stream: %s: %w", videoID, ctx.Err())
		}
		log.Debug().Str("id", videoID).Err(err).Msg("stream yt-dlp failed")
		errs = append(errs, err)
	}
	return Stream{}, fmt.Errorf("stream: %s: %w", videoID, errors.Join(errs...))
}

// viaClient asks as c, in a fresh visitor session when googlevideo
// refuses a URL: YouTube enforces PO tokens on some sessions (about one
// in four, in August 2026) and not others, and a session keeps its lot.
func (y *YouTube) viaClient(ctx context.Context, c Client, videoID string) (Stream, error) {
	for attempt := 1; ; attempt++ {
		s, visitor, err := y.askClient(ctx, c, videoID)
		if !errors.Is(err, errRefused) || attempt == maxSessions || ctx.Err() != nil {
			return s, err
		}
		log.Debug().
			Str("id", videoID).
			Str("client", c.Name).
			Int("attempt", attempt).
			Msg("stream session refused, dropping it")
		y.dropVisitorData(visitor)
	}
}

// askClient also returns the visitorData it used, so viaClient can drop a
// refused session.
func (y *YouTube) askClient(ctx context.Context, c Client, videoID string) (s Stream, visitor string, err error) {
	sent := y.visitorData()
	resp, err := y.player(ctx, c, videoID, sent)
	if err != nil {
		return Stream{}, sent, fmt.Errorf("%s: %w", c.Name, err)
	}
	handedOut := resp.ResponseContext.VisitorData
	refusal := resp.playability(c.Name)
	isBotChecked := refusal != nil && refusal.isBotCheck()
	switch {
	case isBotChecked && handedOut != "" && handedOut != sent:
		// As the web app does: retry in the visitor session the bot check
		// handed out.
		y.setVisitorData(handedOut, true)
		sent = handedOut
		if resp, err = y.player(ctx, c, videoID, sent); err != nil {
			return Stream{}, sent, fmt.Errorf("%s: %w", c.Name, err)
		}
		refusal = resp.playability(c.Name)
	case handedOut != "":
		y.setVisitorData(handedOut, false)
	}
	if refusal != nil {
		return Stream{}, sent, refusal
	}
	// InnerTube answers a blocked IP with another video's response.
	if id := resp.VideoDetails.VideoID; id != "" && id != videoID {
		return Stream{}, sent, fmt.Errorf("%s: got the player response of %s", c.Name, id)
	}
	if s, err = resp.stream(c, y.now()); err != nil {
		return Stream{}, sent, err
	}
	if err := y.probe(ctx, s); err != nil {
		return Stream{}, sent, fmt.Errorf("%s: %w", c.Name, err)
	}
	return s, sent, nil
}

func (y *YouTube) visitorData() string {
	y.mu.Lock()
	defer y.mu.Unlock()
	return y.visitor
}

// dropVisitorData ends the session, unless another resolve already moved
// on from it; the next player request is bot-checked into a new one.
func (y *YouTube) dropVisitorData(visitorData string) {
	y.mu.Lock()
	if y.visitor == visitorData {
		y.visitor = ""
	}
	y.mu.Unlock()
}

// setVisitorData keeps the first session it learns unless replace:
// InnerTube echoes it back with a fresh timestamp, so later ones are no
// news.
func (y *YouTube) setVisitorData(visitorData string, replace bool) {
	y.mu.Lock()
	if replace || y.visitor == "" {
		y.visitor = visitorData
	}
	y.mu.Unlock()
}

// probe range-requests the end of the file: a URL can be issued and still
// be refused, and the engine would only find out a MiB into the song.
func (y *YouTube) probe(ctx context.Context, s Stream) error {
	if y.opts.NoProbe {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return fmt.Errorf("probe: %w", withoutURL(err))
	}
	maps.Copy(req.Header, s.Header)
	start := max(s.Size-probeBytes, 0)
	end := start + probeBytes - 1
	if s.Size > 0 {
		end = min(end, s.Size-1)
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	resp, err := y.opts.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("probe: %w", withoutURL(err))
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, probeBytes))
	switch resp.StatusCode {
	case http.StatusPartialContent:
		return nil
	case http.StatusForbidden:
		return fmt.Errorf("probe: %w (HTTP 403 past its first MiB: a PO token is enforced)", errRefused)
	default:
		return fmt.Errorf("probe: googlevideo answered a range request with HTTP %d", resp.StatusCode)
	}
}

// withoutURL drops the URL net/http puts in its errors: a stream URL
// carries its signature, which must not reach logs or toasts.
func withoutURL(err error) error {
	if urlErr, ok := errors.AsType[*url.Error](err); ok {
		return fmt.Errorf("%s: %w", urlErr.Op, urlErr.Err)
	}
	return err
}

// clone keeps callers from sharing, and racing on, the cached Header.
func (s Stream) clone() Stream {
	s.Header = s.Header.Clone()
	return s
}
