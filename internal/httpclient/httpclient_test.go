package httpclient

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newServer serves HTTP/2 over TLS and counts connections.
func newServer(t *testing.T, h http.HandlerFunc) (srv *httptest.Server, conns *atomic.Int32) {
	t.Helper()
	conns = &atomic.Int32{}
	srv = httptest.NewUnstartedServer(h)
	srv.EnableHTTP2 = true
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, conns
}

// trusting returns New's client, trusting srv's certificate.
func trusting(srv *httptest.Server) *http.Client {
	c := New(0)
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	c.Transport.(*http.Transport).TLSClientConfig.RootCAs = roots
	return c
}

// get reads url, returning its TLS state.
func get(t *testing.T, c *http.Client, url string) *tls.ConnectionState {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Error(err)
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.TLS
}

// Requests in flight together on a cold host share one connection.
func TestNewDialsOnce(t *testing.T) {
	const requests = 4
	var arrived sync.WaitGroup
	arrived.Add(requests)
	srv, conns := newServer(t, func(_ http.ResponseWriter, r *http.Request) {
		arrived.Done()
		arrived.Wait()
		if r.ProtoMajor != 2 {
			t.Errorf("request over %s, want HTTP/2", r.Proto)
		}
	})
	c := trusting(srv)
	var wg sync.WaitGroup
	for range requests {
		wg.Go(func() { get(t, c, srv.URL) })
	}
	wg.Wait()
	if n := conns.Load(); n != 1 {
		t.Errorf("%d requests opened %d connections, want 1", requests, n)
	}
}

func TestNewResumesTLS(t *testing.T) {
	srv, conns := newServer(t, func(http.ResponseWriter, *http.Request) {})
	c := trusting(srv)
	get(t, c, srv.URL)
	c.CloseIdleConnections()
	state := get(t, c, srv.URL)
	if state == nil {
		return
	}
	if n := conns.Load(); n != 2 {
		t.Fatalf("%d connections, want a second one", n)
	}
	if !state.DidResume {
		t.Error("the reconnect did a full TLS handshake, want the session resumed")
	}
}

func TestNewTimeout(t *testing.T) {
	if got := New(time.Second).Timeout; got != time.Second {
		t.Errorf("Timeout = %v, want 1s", got)
	}
	if got := New(0).Timeout; got != 0 {
		t.Errorf("Timeout = %v, want none", got)
	}
}
