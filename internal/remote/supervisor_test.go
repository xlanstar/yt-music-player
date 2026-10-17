package remote

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/notice"
)

// supervised starts a Supervisor of a front-end of version ours over
// dialer, closed when the test ends.
func supervised(t *testing.T, d *dialer, ours string, opts Options) (*front, *Supervisor) {
	t.Helper()
	f := newFront()
	opts.Role, opts.Version, opts.Dialer = api.RoleTUI, ours, d
	if opts.Spawn == nil {
		opts.Spawn = d.spawn
	}
	if opts.isAlive == nil {
		opts.isAlive = func(int) bool { return false }
	}
	if opts.kill == nil {
		opts.kill = func(int) error { return nil }
	}
	s := Supervise(f.client, opts)
	t.Cleanup(func() {
		s.Close()
		d.stop()
	})
	return f, s
}

// settle lets the goroutines run, then the front-end's thread.
func settle(f *front) {
	synctest.Wait()
	f.ui.run()
}

func wantHealths(t *testing.T, f *front, want ...Health) {
	t.Helper()
	if got := f.healths(); !slices.Equal(got, want) {
		t.Errorf("healths %v, want %v", got, want)
	}
}

func TestSupervisorConnects(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDaemon("1.0.0")
		dl := &dialer{running: d}
		f, s := supervised(t, dl, "1.0.0", Options{})
		settle(f)
		wantHealths(t, f, Connected)
		if s.shown.Daemon.Hello.PID != daemonPID {
			t.Errorf("Link %+v", s.shown)
		}
		if h := d.greetings(); len(h) != 1 || !h[0].Subscribe || h[0].Role != api.RoleTUI || h[0].Proto != api.Proto {
			t.Errorf("hello %+v", h)
		}
		if err := ping(t.Context(), f.client); err != nil {
			t.Error(err)
		}
	})
}

// Attempts that fail wait 0.25 s, doubling up to 30 s, for ever; the
// third failure in a row makes the daemon Unavailable.
func TestSupervisorBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dl := &dialer{}
		f, _ := supervised(t, dl, "1.0.0", Options{})
		start := time.Now()
		time.Sleep(2 * time.Minute)
		settle(f)
		var at []time.Duration
		for _, l := range dl.log {
			if l.what == "dial" {
				at = append(at, l.at.Sub(start))
			}
		}
		const ms = time.Millisecond
		want := []time.Duration{
			0, 250 * ms, 750 * ms, 1750 * ms, 3750 * ms, 7750 * ms, 15750 * ms, 31750 * ms,
			61750 * ms, 91750 * ms,
		}
		if !slices.Equal(at, want) {
			t.Errorf("dialed at %v, want %v", at, want)
		}
		wantHealths(t, f, Unavailable)
	})
}

// Retry and Foreground connect at once and wait from the shortest
// backoff again.
func TestSupervisorRetryResetsBackoff(t *testing.T) {
	for _, tc := range []struct {
		name string
		do   func(*Supervisor)
	}{
		{"Retry", (*Supervisor).Retry},
		{"Foreground", (*Supervisor).Foreground},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				dl := &dialer{}
				f, s := supervised(t, dl, "1.0.0", Options{})
				time.Sleep(20 * time.Second) // waiting 16 s now
				synctest.Wait()
				before := dl.count("dial")
				tc.do(s)
				synctest.Wait()
				if dl.count("dial") != before+1 {
					t.Fatal("no attempt at once")
				}
				time.Sleep(250 * time.Millisecond)
				synctest.Wait()
				if dl.count("dial") != before+2 {
					t.Error("the backoff did not start over")
				}
				// Unavailable until connected.
				settle(f)
				d := newDaemon("1.0.0")
				dl.setRunning(d)
				tc.do(s)
				settle(f)
				wantHealths(t, f, Unavailable, Connected)
			})
		})
	}
}

// The connection's end fails the calls in flight, then connects again,
// starting a daemon.
func TestSupervisorReconnects(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		first := newDaemon("1.0.0")
		first.answer = func(*api.Call) *api.Reply { return nil }
		second := newDaemon("1.0.0")
		dl := &dialer{running: first, next: []*daemon{second}}
		f, _ := supervised(t, dl, "1.0.0", Options{})
		settle(f)
		errc := make(chan error, 1)
		go func() {
			_, err := f.client.Home(t.Context())
			errc <- err
		}()
		synctest.Wait()
		dl.stop()
		if err := <-errc; !errors.Is(err, api.ErrDisconnected) {
			t.Errorf("Home = %v, want ErrDisconnected", err)
		}
		settle(f)
		// Reconnected before the front-end's thread ran.
		wantHealths(t, f, Connected, Connected)
		if c := f.links[1].Connection; c != 2 {
			t.Errorf("Connection %d, want 2", c)
		}
		if dl.count("spawn") != 1 {
			t.Errorf("%d spawns, want 1", dl.count("spawn"))
		}
		if err := ping(t.Context(), f.client); err != nil {
			t.Error(err)
		}
	})
}

// A Ping unanswered in time makes the daemon Unresponsive, and any frame
// Connected again. No Ping goes while idle.
func TestSupervisorUnresponsive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDaemon("1.0.0")
		d.answer = func(*api.Call) *api.Reply { return nil }
		dl := &dialer{running: d}
		f, s := supervised(t, dl, "1.0.0", Options{})
		time.Sleep(time.Hour)
		settle(f)
		if len(d.calls()) != 0 {
			t.Fatalf("calls %v while idle", d.calls())
		}
		s.Foreground()
		time.Sleep(pingTimeout)
		settle(f)
		wantHealths(t, f, Connected, Unresponsive)
		if got := d.calls(); !slices.Equal(got, []api.Method{api.MethodPing}) {
			t.Errorf("calls %v, want a Ping", got)
		}
		d.send(api.Frame{Event: &api.Event{State: &api.State{}}})
		settle(f)
		wantHealths(t, f, Connected, Unresponsive, Connected)
	})
}

// A control unanswered in time is told, and pings the daemon.
func TestSupervisorControlTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDaemon("1.0.0")
		d.answer = func(*api.Call) *api.Reply { return nil }
		dl := &dialer{running: d}
		f, _ := supervised(t, dl, "1.0.0", Options{})
		settle(f)
		f.client.Next()
		time.Sleep(controlTimeout)
		settle(f)
		if len(f.notices) != 1 || f.notices[0].Title != notice.ControlFailed {
			t.Errorf("notices %+v, want ControlFailed", f.notices)
		}
		time.Sleep(pingTimeout)
		settle(f)
		if got := d.calls(); !slices.Equal(got, []api.Method{api.MethodNext, api.MethodPing}) {
			t.Errorf("calls %v, want Next then Ping", got)
		}
		wantHealths(t, f, Connected, Unresponsive)
	})
}

// processes fakes the OS's: the daemon runs until killed, or for ever
// after it took a shutdown if it is stuck.
type processes struct {
	mu     sync.Mutex
	alive  map[int]bool
	killed []int
	// onKill runs after a kill: the daemon's socket goes with it.
	onKill func()
}

func (p *processes) isAlive(pid int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.alive[pid]
}

func (p *processes) kill(pid int) error {
	p.mu.Lock()
	p.killed = append(p.killed, pid)
	delete(p.alive, pid)
	p.mu.Unlock()
	if p.onKill != nil {
		p.onKill()
	}
	return nil
}

func (p *processes) exit(pid int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.alive, pid)
}

func (p *processes) kills() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.killed)
}

func TestSupervisorRestart(t *testing.T) {
	for _, tc := range []struct {
		name string
		// isStuck: the daemon answers nothing and never exits.
		isStuck  bool
		wantKill bool
	}{
		{"stops", false, false},
		{"stuck", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				procs := &processes{alive: map[int]bool{daemonPID: true}}
				old := newDaemon("1.0.0")
				old.answer = func(c *api.Call) *api.Reply {
					if tc.isStuck {
						return nil
					}
					if c.Method == api.MethodShutdown {
						procs.exit(daemonPID)
					}
					return &api.Reply{}
				}
				fresh := newDaemon("1.0.0")
				dl := &dialer{running: old, next: []*daemon{fresh}}
				procs.onKill = dl.stop
				f, s := supervised(t, dl, "1.0.0", Options{isAlive: procs.isAlive, kill: procs.kill})
				settle(f)
				start := time.Now()
				s.Restart()
				synctest.Wait()
				if tc.isStuck {
					if len(procs.kills()) != 0 {
						t.Fatal("killed at once")
					}
					time.Sleep(RestartGrace)
				}
				settle(f)
				if got := old.calls(); !slices.Equal(got, []api.Method{api.MethodShutdown}) {
					t.Errorf("calls %v, want Shutdown", got)
				}
				if isKilled := slices.Contains(procs.kills(), daemonPID); isKilled != tc.wantKill {
					t.Errorf("killed %v, want %v", procs.kills(), tc.wantKill)
				}
				if tc.isStuck && time.Since(start) != RestartGrace {
					t.Errorf("killed after %v, want %v", time.Since(start), RestartGrace)
				}
				if len(fresh.greetings()) != 1 || dl.count("spawn") != 1 {
					t.Error("no new daemon started")
				}
				wantHealths(t, f, Connected, Connected)
				if c := s.shown.Connection; c != 2 {
					t.Errorf("Connection %d, want 2", c)
				}
			})
		})
	}
}

// An older daemon is handed off once per version: still older after it,
// it is DaemonOutdated, though usable with the same Proto.
func TestSupervisorHandsOffOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		old := newDaemon("1.1.0")
		alsoOld := newDaemon("1.1.0")
		dl := &dialer{running: old, next: []*daemon{alsoOld}}
		f, s := supervised(t, dl, "1.2.0", Options{})
		settle(f)
		if got := old.calls(); !slices.Equal(got, []api.Method{api.MethodHandoff}) {
			t.Fatalf("old daemon's calls %v, want Handoff", got)
		}
		if hand := old.received()[0].Call.Hand; hand.Wait != HandoffWait {
			t.Errorf("Handoff's wait %v", hand.Wait)
		}
		if got := dl.log; len(got) != 3 || got[1].what != "spawn" || got[2].what != "wait" {
			t.Errorf("dials %+v, want dial, spawn, then wait", got)
		}
		wantHealths(t, f, DaemonOutdated)
		if l := s.shown; l.Daemon.Hello.Version != "1.1.0" || l.Daemon.Version != "1.2.0" {
			t.Errorf("Link %+v, want both versions", l)
		}
		if err := ping(t.Context(), f.client); err != nil {
			t.Errorf("compatible, yet %v", err)
		}

		// The same version again: no second hand-off.
		dl.stop()
		settle(f)
		dl.setRunning(alsoOld)
		time.Sleep(time.Second)
		settle(f)
		if len(alsoOld.greetings()) != 2 || slices.Contains(alsoOld.calls(), api.MethodHandoff) {
			t.Errorf("handed off the same version twice: %v", alsoOld.calls())
		}
		if dl.count("spawn") != 1 {
			t.Errorf("%d spawns, want 1", dl.count("spawn"))
		}
		wantHealths(t, f, DaemonOutdated, Reconnecting, DaemonOutdated)
	})
}

// Told of another front-end's hand-off, a front-end only waits for the
// daemon that one starts; after the wait, it starts one itself.
func TestSupervisorHandoffEvent(t *testing.T) {
	for _, tc := range []struct {
		name      string
		comesIn   time.Duration // 0: never
		wantSpawn int
	}{
		{"new daemon comes", time.Second, 0},
		{"none comes", 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				old := newDaemon("1.0.0")
				fresh := newDaemon("1.0.0")
				dl := &dialer{running: old, next: []*daemon{fresh}}
				f, _ := supervised(t, dl, "1.0.0", Options{})
				settle(f)
				old.send(api.Frame{Event: &api.Event{Handoff: &api.HandoffEvent{By: 7, Wait: HandoffWait}}})
				synctest.Wait()
				dl.stop()
				if tc.comesIn > 0 {
					time.Sleep(tc.comesIn)
					dl.setRunning(fresh)
				}
				time.Sleep(HandoffWait + time.Second)
				settle(f)
				if got := dl.count("spawn"); got != tc.wantSpawn {
					t.Errorf("%d spawns, want %d", got, tc.wantSpawn)
				}
				if len(fresh.greetings()) != 1 {
					t.Error("not connected to the new daemon")
				}
				if dl.log[1].what != "wait" {
					t.Errorf("dials %+v, want a wait first", dl.log)
				}
			})
		})
	}
}

// A newer daemon of another Proto: the front-end is outdated and stops.
func TestSupervisorFrontendOutdated(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDaemon("2.0.0")
		d.hello.Proto = api.Proto + 1
		dl := &dialer{running: d}
		f, _ := supervised(t, dl, "1.0.0", Options{})
		time.Sleep(time.Hour)
		settle(f)
		wantHealths(t, f, FrontendOutdated)
		if dl.count("dial") != 1 || len(d.calls()) != 0 {
			t.Errorf("dials %+v, calls %v; want one and none", dl.log, d.calls())
		}
		if _, err := f.client.Home(t.Context()); !errors.Is(err, api.ErrDisconnected) {
			t.Errorf("Home = %v, want ErrDisconnected", err)
		}
	})
}

// An older daemon of another Proto, after the hand-off: DaemonOutdated,
// and no call goes through.
func TestSupervisorDaemonIncompatible(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		old := newDaemon("0.9.0")
		old.hello.Proto = api.Proto + 1 // a Proto this build cannot read
		alsoOld := newDaemon("0.9.0")
		alsoOld.hello.Proto = api.Proto + 1
		dl := &dialer{running: old, next: []*daemon{alsoOld}}
		f, _ := supervised(t, dl, "1.0.0", Options{})
		settle(f)
		if got := old.calls(); !slices.Equal(got, []api.Method{api.MethodHandoff}) {
			t.Errorf("calls %v, want Handoff, which every Proto knows", got)
		}
		wantHealths(t, f, DaemonOutdated)
		if err := ping(t.Context(), f.client); !errors.Is(err, api.ErrDisconnected) {
			t.Errorf("Ping = %v, want ErrDisconnected", err)
		}
	})
}

func TestSupervisorClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDaemon("1.0.0")
		dl := &dialer{running: d}
		f, s := supervised(t, dl, "1.0.0", Options{})
		settle(f)
		s.Close()
		if _, err := f.client.Home(t.Context()); !errors.Is(err, api.ErrDisconnected) {
			t.Errorf("Home after Close = %v", err)
		}
		if got := d.calls(); len(got) != 0 {
			t.Errorf("calls %v: closing leaves the daemon alone", got)
		}
	})
}
