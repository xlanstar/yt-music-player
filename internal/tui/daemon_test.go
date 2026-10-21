package tui

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"yt-music-player/internal/accountcache"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/daemon"
	"yt-music-player/internal/remote"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// pipeDialer connects to an in-process Service over net.Pipe.
type pipeDialer struct{ svc *daemon.Service }

func (d pipeDialer) Dial(context.Context) (net.Conn, error) {
	ours, theirs := net.Pipe()
	go d.svc.Serve(theirs) //nolint:contextcheck // the Service's end outlives the dial, as a socket's does
	return ours, nil
}

func (d pipeDialer) Wait(ctx context.Context) (net.Conn, error)        { return d.Dial(ctx) }
func (d pipeDialer) DialOrSpawn(ctx context.Context) (net.Conn, error) { return d.Dial(ctx) }

// daemonApp is the TUI's app wired as Run wires it, over an in-process
// Service of the placeholder's music; the test's goroutine runs Update.
type daemonApp struct {
	t *testing.T
	a *app
}

// signingSource is the placeholder's music, over which anyone signs in.
type signingSource struct{ catalog.Source }

func (signingSource) SignIn(context.Context, string) (catalog.Account, error) {
	return catalog.Account{Name: "Grace Hopper"}, nil
}

func (signingSource) SignOut() error { return nil }

func newDaemonApp(t *testing.T) *daemonApp {
	t.Helper()
	return newCachingApp(t, "")
}

// newCachingApp is newDaemonApp keeping the account's library and likes
// in accountCache.
func newCachingApp(t *testing.T, accountCache string) *daemonApp {
	t.Helper()
	svc := daemon.New(daemon.Options{
		Source: signingSource{catalog.NewPlaceholderSource(0)},
		Seed:   catalog.Placeholder().Songs,
	})
	svc.Start()
	a := newApp(Config{
		Dialer: pipeDialer{svc}, Version: "1.2.3", DaemonLog: "/logs/ytmd.log", AccountCache: accountCache,
		Spawn: func() error { return errors.New("no ytmd to start") },
	}, nil)
	t.Cleanup(func() {
		a.close()
		svc.Shutdown()
	})
	d := &daemonApp{t: t, a: a}
	d.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	return d
}

func (d *daemonApp) send(msg tea.Msg) {
	d.t.Helper()
	d.a.m.Update(msg)
}

// until runs what is posted, as one message per wake as Run does, until
// cond holds.
func (d *daemonApp) until(what string, cond func() bool) {
	d.t.Helper()
	deadline := time.After(5 * time.Second)
	for !cond() {
		select {
		case <-d.a.posts.wake:
			d.send(postedMsg{})
		case <-deadline:
			d.t.Fatalf("never %s:\n%s", what, d.a.m.content)
		}
	}
}

func (d *daemonApp) shows(text string) bool { return strings.Contains(ansi.Strip(d.a.m.content), text) }

// The TUI over a daemon: the snapshot shows, controls go through, and
// the daemon's events come back as one message per burst.
func TestOverDaemon(t *testing.T) {
	d := newDaemonApp(t)
	m := d.a.m
	d.until("connected", func() bool { return m.link.Health == remote.Connected })
	d.until("showed the queue and the account", func() bool {
		return d.shows("Satellite Hearts") && d.shows("Lan (@lan)")
	})

	d.send(keyMsg("space"))
	// Without a resolver, the song loads for ever: the progress stands
	// still, and nothing ticks.
	d.until("played", func() bool { return m.mirror.State().Playing })
	d.until("showed it loading", func() bool { return d.shows("… Midnight Arcade") })
	if m.isTicking {
		t.Error("ticking while the song loads")
	}

	d.send(keyMsg("down"))
	d.send(keyMsg("l"))
	d.until("liked", func() bool { return m.session.IsLiked("satellite-hearts") && !m.session.IsBusy() })

	// Down skips the heading over the playlists.
	d.send(keyMsg("shift+tab"))
	d.send(keyMsg("down"))
	d.send(keyMsg("enter"))
	d.until("opened a playlist", func() bool { return d.shows("Late Night Drive") && d.shows("Play all") })
}

// The library and likes fetched are kept for the next run.
func TestAccountCached(t *testing.T) {
	file := accountcache.File(filepath.Join(t.TempDir(), "account.json"))
	d := newCachingApp(t, string(file))
	m := d.a.m
	d.until("fetched the account", func() bool { return m.session.IsSignedIn() && !m.session.IsBusy() })
	saved, err := file.Load()
	if err != nil || saved.Account.Name != "Lan" || len(saved.Liked) != m.session.LikedCount() || len(saved.Liked) == 0 {
		t.Errorf("kept %+v, %d liked, %v", saved.Account, len(saved.Liked), err)
	}
}

// Signing in switches accounts: the daemon empties its queue, the
// previous account's, and stops; the TUI shows nothing of it.
func TestSignInEmptiesTheQueue(t *testing.T) {
	d := newDaemonApp(t)
	m := d.a.m
	d.until("showed the queue", func() bool { return d.shows("Satellite Hearts") && !m.session.IsBusy() })
	d.send(keyMsg("space"))
	d.until("played", func() bool { return m.mirror.State().Playing && d.shows("… Midnight Arcade") })

	m.session.SignIn("SID=good")
	d.until("signed in", func() bool { return d.shows("Grace Hopper") && len(m.mirror.Queue()) == 0 })
	for _, text := range []string{"Midnight Arcade", "Satellite Hearts"} {
		if d.shows(text) {
			t.Errorf("%q still shows:\n%s", text, m.content)
		}
	}
	if !d.shows("Nothing playing") || !d.shows("The queue is empty") || m.mirror.State().Playing || m.wantsTicks() {
		t.Errorf("the player plays on:\n%s", m.content)
	}
}

// post never blocks, from any goroutine, and one message runs all the
// funcs posted before it, in order.
func TestPoster(t *testing.T) {
	p := newPoster()
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 100 {
				p.post(func() {})
			}
		})
	}
	wg.Wait()
	var order []int
	for i := range 3 {
		p.post(func() { order = append(order, i) })
	}
	ran := 0
	p.post(func() { ran++ })
	<-p.wake
	p.run()
	if ran != 1 || len(order) != 3 || order[2] != 2 {
		t.Errorf("ran %d, order %v", ran, order)
	}
	select {
	case <-p.wake:
		t.Error("woken again for nothing new")
	default:
	}

	// pump sends a message per wake until its context ends.
	ctx, cancel := context.WithCancel(context.Background())
	sent := make(chan tea.Msg, 1)
	done := make(chan struct{})
	go func() {
		p.pump(ctx, func(msg tea.Msg) { sent <- msg })
		close(done)
	}()
	p.post(func() {})
	if _, ok := (<-sent).(postedMsg); !ok {
		t.Error("pump sent another message")
	}
	cancel()
	<-done
}
