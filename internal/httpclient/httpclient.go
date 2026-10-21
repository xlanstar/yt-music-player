// Package httpclient makes a process's HTTP client, tuned for YouTube's
// hosts, which all speak HTTP/2 (docs/performance.md, Network).
package httpclient

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

// idleTimeout: YouTube keeps idle connections 1.5–5 minutes, Go 90 s.
const idleTimeout = 2 * time.Minute

// New returns a client over a Transport of its own; share one per process.
// A zero timeout means none, as streams need.
func New(timeout time.Duration) *http.Client {
	t := defaultTransport()
	// Requests starting together on a new host would each dial, all but
	// one connection dropped once HTTP/2 is agreed.
	t.MaxConnsPerHost = 1
	t.IdleConnTimeout = idleTimeout
	// Go resumes no TLS sessions by default.
	t.TLSClientConfig = &tls.Config{ClientSessionCache: tls.NewLRUClientSessionCache(64)}
	return &http.Client{Transport: t, Timeout: timeout}
}

// defaultTransport copies http.DefaultTransport or, where a program put
// another RoundTripper there, makes one with net/http's defaults.
func defaultTransport() *http.Transport {
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		return t.Clone()
	}
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}
