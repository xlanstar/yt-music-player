package tui

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
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

func newDaemonApp(t *testing.T) *daemonApp {
	t.Helper()
	svc, err := daemon.New(daemon.Options{
		Source:  catalog.NewPlaceholderSource(0),
		Seed:    catalog.Placeholder().Songs,
		Version: "1.2.3",
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.Start()
	a := newApp(Config{
		Dialer: pipeDialer{svc}, Version: "1.2.3", DaemonLog: "/logs/ytmd.log",
		Spawn: func() error { return errors.New("no ytmd to start") },
	})
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

	d.send(keyMsg("tab"))
	d.send(keyMsg("/"))
	for _, r := range "moon" {
		d.send(keyMsg(string(r)))
	}
	d.until("found", func() bool { return d.shows("Slow Burn Summer") })
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
