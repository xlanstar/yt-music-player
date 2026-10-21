// Package innertube is a transport for InnerTube, the JSON API behind the
// YouTube Music web app. It knows nothing about music.
package innertube

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// DefaultBaseURL is where InnerTube endpoints live.
const DefaultBaseURL = "https://music.youtube.com/youtubei/v1/"

// Origin is the site InnerTube expects requests from.
const Origin = "https://music.youtube.com"

// UserAgent is a desktop browser's: InnerTube may answer non-browsers
// differently from WEB_REMIX.
const UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

const (
	clientName = "WEB_REMIX"
	// maxResponse keeps a misbehaving server from exhausting memory; the
	// largest real pages are a few MB.
	maxResponse = 64 << 20
	// maxRetryAfter keeps a hostile Retry-After from stalling a caller
	// whose ctx has no deadline.
	maxRetryAfter = 30 * time.Second
)

// ErrUnauthorized is what a StatusError unwraps to when InnerTube refused
// the credentials: a 401, or a 403 to a signed call.
var ErrUnauthorized = errors.New("innertube: unauthorized")

// Client calls InnerTube. Configure it before first use; it is then safe
// for concurrent use. A copy shares the original's Auth.
type Client struct {
	// HTTP defaults to http.DefaultClient.
	HTTP *http.Client
	// BaseURL defaults to DefaultBaseURL.
	BaseURL string
	// HL and GL are the language and region, like "en" and "US". An empty
	// GL lets InnerTube infer the region from the IP.
	HL, GL string
	// VisitorID, sent as X-Goog-Visitor-Id, keeps responses consistent
	// for one visitor.
	VisitorID string
	// ClientVersion overrides the WEB_REMIX version derived from today's
	// date, which is what the web app sends and InnerTube accepts.
	ClientVersion string
	// Auth signs each attempt with its credentials at that moment; they
	// may change while calls run.
	Auth *Auth
	// Retries is how often a 429 or 5xx is retried; Backoff is the first
	// delay, doubled each retry. Zero means 3 and 500ms; a negative
	// Retries disables retrying.
	Retries int
	Backoff time.Duration
}

// New returns a client for English responses.
func New() *Client { return &Client{HL: "en"} }

// Auth holds the credentials a Client signs calls with. The zero value
// signs nothing; it is safe for concurrent use.
type Auth struct {
	sign atomic.Pointer[func(*http.Request) error]
}

// Set makes later attempts call sign just before sending (to add cookies
// and SAPISIDHASH); its error aborts the call. nil signs out.
func (a *Auth) Set(sign func(*http.Request) error) {
	if sign == nil {
		a.sign.Store(nil)
		return
	}
	a.sign.Store(&sign)
}

// IsSignedIn reports whether calls are signed; a nil Auth is not.
func (a *Auth) IsSignedIn() bool { return a.load() != nil }

func (a *Auth) load() func(*http.Request) error {
	if a == nil {
		return nil
	}
	if sign := a.sign.Load(); sign != nil {
		return *sign
	}
	return nil
}

// StatusError is a non-2xx response.
type StatusError struct {
	Endpoint   string
	StatusCode int
	// Message is InnerTube's error.message, if any.
	Message string
	// Authenticated reports whether the attempt was signed.
	Authenticated bool
}

func (e *StatusError) Error() string {
	msg := fmt.Sprintf("innertube: %s: HTTP %d %s", e.Endpoint, e.StatusCode, http.StatusText(e.StatusCode))
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}

// Unwrap makes errors.Is(err, ErrUnauthorized) match refused credentials.
func (e *StatusError) Unwrap() error {
	if e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden && e.Authenticated {
		return ErrUnauthorized
	}
	return nil
}

// Call posts body to endpoint (like "search" or "browse") with the client
// context added, and hands the body of the response that succeeds to
// decode, returning its error as is. body is not modified and must not
// hold "context".
func (c *Client) Call(ctx context.Context, endpoint string, body map[string]any, decode func(io.Reader) error) error {
	data, err := c.payload(body)
	if err != nil {
		return fmt.Errorf("innertube: %s: %w", endpoint, err)
	}
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	url := strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(endpoint, "/") + "?prettyPrint=false"

	retries, backoff := c.Retries, c.Backoff
	if retries == 0 {
		retries = 3
	}
	if backoff <= 0 {
		backoff = 500 * time.Millisecond
	}
	for attempt := 0; ; attempt++ {
		wait, err := c.do(ctx, endpoint, url, data, decode)
		if err == nil || attempt >= retries || !retryable(err) {
			return err
		}
		wait = max(wait, min(backoff<<attempt, maxRetryAfter))
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("innertube: %s: %w (after %w)", endpoint, ctx.Err(), err)
		case <-timer.C:
		}
	}
}

// retryable reports a 429 or a 5xx: only those may succeed on retry.
func retryable(err error) bool {
	statusErr, isStatus := errors.AsType[*StatusError](err)
	if !isStatus {
		return false
	}
	return statusErr.StatusCode == http.StatusTooManyRequests || statusErr.StatusCode >= 500
}

// payload embeds body rather than copying it into a map beside the
// context, which allocated a third more (BenchmarkPayload).
type payload struct {
	Context requestContext `json:"context"`
	Body    map[string]any `json:",embed"`
}

func (c *Client) payload(body map[string]any) ([]byte, error) {
	return json.Marshal(payload{Context: c.context(), Body: body})
}

// do sends one attempt; wait is the server's Retry-After, if any.
func (c *Client) do(
	ctx context.Context,
	endpoint, url string,
	data []byte,
	decode func(io.Reader) error,
) (wait time.Duration, err error) {
	req, signed, err := c.newRequest(ctx, endpoint, url, data)
	if err != nil {
		return 0, err
	}

	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("innertube: %s: %w", endpoint, err)
	}
	// The body is read in full, so closing cannot lose anything.
	defer func() { _ = resp.Body.Close() }()
	body := io.LimitReader(resp.Body, maxResponse)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg, err := io.ReadAll(body)
		if err != nil {
			return 0, fmt.Errorf("innertube: %s: %w", endpoint, err)
		}
		statusErr := &StatusError{
			Endpoint:      endpoint,
			StatusCode:    resp.StatusCode,
			Message:       errorMessage(msg),
			Authenticated: signed,
		}
		return retryAfter(resp.Header.Get("Retry-After")), statusErr
	}
	// Decoded as it is read: io.ReadAll's growing copies were about 3/4 of
	// a page's allocated bytes.
	err = decode(body)
	_, _ = io.Copy(io.Discard, body)
	return 0, err
}

func (c *Client) newRequest(
	ctx context.Context,
	endpoint, url string,
	data []byte,
) (req *http.Request, signed bool, err error) {
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, false, fmt.Errorf("innertube: %s: %w", endpoint, err)
	}
	h := req.Header
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "*/*")
	h.Set("User-Agent", UserAgent)
	h.Set("Origin", Origin)
	h.Set("Referer", Origin+"/")
	// SOCS=CAI declines the EU consent interstitial, as ytmusicapi does.
	h.Set("Cookie", "SOCS=CAI")
	if c.VisitorID != "" {
		h.Set("X-Goog-Visitor-Id", c.VisitorID)
	}
	if sign := c.Auth.load(); sign != nil {
		if err := sign(req); err != nil {
			return nil, false, fmt.Errorf("innertube: %s: auth: %w", endpoint, err)
		}
		signed = true
	}
	return req, signed, nil
}

// requestContext is a struct, not maps: maps took 8 of a payload's 16
// allocations (BenchmarkPayload, BenchmarkContext).
type requestContext struct {
	// A trailing zero-size field would add padding after Client.
	User   struct{} `json:"user"`
	Client struct {
		ClientName    string `json:"clientName"`
		ClientVersion string `json:"clientVersion"`
		HL            string `json:"hl,omitzero"`
		GL            string `json:"gl,omitzero"`
	} `json:"client"`
}

func (c *Client) context() requestContext {
	var rc requestContext
	rc.Client.ClientName, rc.Client.ClientVersion = clientName, c.ClientVersion
	if rc.Client.ClientVersion == "" {
		rc.Client.ClientVersion = "1." + time.Now().UTC().Format("20060102") + ".01.00"
	}
	rc.Client.HL, rc.Client.GL = c.HL, c.GL
	return rc
}

func errorMessage(body []byte) string {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return ""
	}
	return envelope.Error.Message
}

// retryAfter reads a Retry-After in seconds; HTTP dates are rare from
// InnerTube and fall back to the client's own backoff.
func retryAfter(header string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || seconds <= 0 {
		return 0
	}
	return min(time.Duration(seconds)*time.Second, maxRetryAfter)
}
