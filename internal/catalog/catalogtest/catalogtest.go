// Package catalogtest provides a catalog.Source and catalog.Accounts whose
// requests tests answer by hand.
package catalogtest

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
	"yt-music-player/internal/catalog"
)

// Call is a request to a Source; it blocks until the test sends a Reply.
type Call struct {
	// Kind is "album", "playlist", "search", "home", "explore", "account",
	// "library", "like", "signin" or "signout". ID is the ID, the query,
	// "+id" to like or "-id" to unlike, or the credentials.
	Kind, ID string
	Ctx      context.Context
	Reply    chan Reply
}

// Reply answers a Call with the fields of its kind, or Err.
type Reply struct {
	Album    catalog.Album
	Playlist catalog.Playlist
	Songs    []catalog.Song
	Results  catalog.Results
	Home     catalog.Home
	Explore  catalog.Explore
	Account  catalog.Account
	Library  catalog.Library
	Err      error
}

// Source hands each request to the test to answer. It answers even after
// cancellation, as a real source may, so callers must drop stale results.
type Source struct {
	Calls chan Call
}

var (
	_ catalog.Source   = Source{}
	_ catalog.Accounts = Accounts{}
)

// New returns a Source whose Calls channel buffers a burst of concurrent
// requests.
func New() Source { return Source{Calls: make(chan Call, 16)} }

func (s Source) ask(ctx context.Context, kind, id string) Reply {
	call := Call{kind, id, ctx, make(chan Reply, 1)}
	s.Calls <- call
	return <-call.Reply
}

// Search asks for "search query".
func (s Source) Search(ctx context.Context, query string) (catalog.Results, error) {
	reply := s.ask(ctx, "search", query)
	return reply.Results, reply.Err
}

// Album asks for "album id".
func (s Source) Album(ctx context.Context, id string) (catalog.Album, []catalog.Song, error) {
	reply := s.ask(ctx, "album", id)
	return reply.Album, reply.Songs, reply.Err
}

// Playlist asks for "playlist id".
func (s Source) Playlist(ctx context.Context, id string) (catalog.Playlist, []catalog.Song, error) {
	reply := s.ask(ctx, "playlist", id)
	return reply.Playlist, reply.Songs, reply.Err
}

// Home asks for "home".
func (s Source) Home(ctx context.Context) (catalog.Home, error) {
	reply := s.ask(ctx, "home", "")
	return reply.Home, reply.Err
}

// Explore asks for "explore".
func (s Source) Explore(ctx context.Context) (catalog.Explore, error) {
	reply := s.ask(ctx, "explore", "")
	return reply.Explore, reply.Err
}

// Account asks for "account".
func (s Source) Account(ctx context.Context) (catalog.Account, error) {
	reply := s.ask(ctx, "account", "")
	return reply.Account, reply.Err
}

// Library asks for "library".
func (s Source) Library(ctx context.Context) (catalog.Library, error) {
	reply := s.ask(ctx, "library", "")
	return reply.Library, reply.Err
}

// Like asks for "like +id", or "like -id" to unlike.
func (s Source) Like(ctx context.Context, id string, liked bool) error {
	if liked {
		return s.ask(ctx, "like", "+"+id).Err
	}
	return s.ask(ctx, "like", "-"+id).Err
}

// Accounts is a Source that also signs in and out.
type Accounts struct{ Source }

// SignIn asks for "signin credentials".
func (a Accounts) SignIn(ctx context.Context, credentials string) (catalog.Account, error) {
	reply := a.ask(ctx, "signin", credentials)
	return reply.Account, reply.Err
}

// SignOut asks for "signout".
func (a Accounts) SignOut() error { return a.ask(context.Background(), "signout", "").Err }

// tick is how often waits call idle, so the app builds frames meanwhile.
const tick = 10 * time.Millisecond

// await hands each request arriving within wait to handle until handle
// returns true, calling idle while none arrives. It reports whether handle
// returned true in time.
func (s Source) await(idle func(), wait time.Duration, handle func(Call) bool) bool {
	timeout := time.After(wait)
	for {
		select {
		case call := <-s.Calls:
			if handle(call) {
				return true
			}
		case <-time.After(tick):
			if idle != nil {
				idle()
			}
		case <-timeout:
			return false
		}
	}
}

// Next waits for the next request, which must match kind and id, calling
// idle while none arrives.
func (s Source) Next(t testing.TB, idle func(), kind, id string) Call {
	t.Helper()
	var got Call
	ok := s.await(idle, 5*time.Second, func(call Call) bool {
		got = call
		return true
	})
	if !ok {
		t.Fatalf("no request for %s %q", kind, id)
	}
	if got.Kind != kind || got.ID != id {
		t.Fatalf("asked for %s %q, want %s %q", got.Kind, got.ID, kind, id)
	}
	return got
}

// Take waits for concurrent requests in any order, each given as kind and
// ID ("playlist LM", "account"), and returns them keyed the same way.
func (s Source) Take(t testing.TB, idle func(), want ...string) map[string]Call {
	t.Helper()
	got := map[string]Call{}
	ok := s.await(idle, 5*time.Second, func(call Call) bool {
		key := strings.TrimSpace(call.Kind + " " + call.ID)
		if !slices.Contains(want, key) || got[key].Reply != nil {
			t.Fatalf("unexpected request for %s, want %q", key, want)
		}
		got[key] = call
		return len(got) == len(want)
	})
	if !ok {
		t.Fatalf("got requests %v, want %q", slices.Collect(maps.Keys(got)), want)
	}
	return got
}

// None fails t if any request arrives within wait.
func (s Source) None(t testing.TB, idle func(), wait time.Duration) {
	t.Helper()
	s.await(idle, wait, func(call Call) bool {
		t.Fatalf("unexpected request for %s %q", call.Kind, call.ID)
		return true
	})
}
