package main

import (
	"context"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/job"
	"yt-music-player/internal/notice"
	"yt-music-player/internal/remote"

	"github.com/egoist/mygo/ui"
	"github.com/rs/zerolog/log"
)

// ytmd is how the app reaches the daemon: its socket, or in tests an
// in-process Service over a pipe.
type ytmd struct {
	dialer remote.Dialer
	// spawn starts the app's own ytmd after a hand-off; nil is
	// ipc.SpawnDaemon.
	spawn   func() error
	version string
	// log is the daemon's log file, which the banner's View log opens; ""
	// offers none.
	log string
}

// daemonState is what the app knows of ytmd besides playback, which the
// Mirror holds.
type daemonState struct {
	ytmd ytmd
	link remote.Link
	// stopping stops ytmd for Quit and Stop Playback; quit then quits,
	// mygo.App.Quit but in tests.
	stopping job.Job
	quit     func()
}

// daemonHandler routes what the daemon reports: the account's events to
// the session, notices to tell, the Link to the banner.
func (a *app) daemonHandler() remote.Handler {
	return remote.Handler{
		Account: func(e api.AccountEvent) { a.session.ApplyAccount(e) },
		Library: func(l catalog.Library) { a.session.ApplyLibrary(l) },
		Likes:   func(l api.Likes) { a.session.ApplyLikes(l) },
		Failed:  func(e api.FailedEvent) { a.session.ApplyFailed(e) },
		Notice:  a.tellLater,
		Link:    a.linked,
	}
}

// connect has a Supervisor keep the client connected to d, starting ytmd
// when none answers. Call it once.
func (a *app) connect(d ytmd) {
	a.daemon.ytmd = d
	a.sup = remote.Supervise(a.client, remote.Options{
		Role: api.RoleDesktop, Version: d.version, Dialer: d.dialer, Spawn: d.spawn,
	})
}

// linked keeps the connection's state for the banner. A new connection
// after the first sent the daemon's state anew: what failed meanwhile is
// fetched again.
func (a *app) linked(l remote.Link) {
	isAgain := a.daemon.link.Connection > 0 && l.Connection > a.daemon.link.Connection
	a.daemon.link = l
	if isAgain {
		a.retryFailed()
	}
}

// tellLater tells n in the next build: tell needs its Context.
func (a *app) tellLater(n notice.Notice) { a.notices = append(a.notices, n) }

func (a *app) syncNotices(c *ui.Context) {
	for _, n := range a.notices {
		a.tell(c, n)
	}
	clear(a.notices)
	a.notices = a.notices[:0]
}

// foreground tells the Supervisor the window came to the front: it checks
// that the daemon answers, or connects at once.
func (a *app) foreground() {
	if a.sup != nil {
		a.sup.Foreground()
	}
}

// disconnect stops connecting, as the window closes; the daemon plays on.
func (a *app) disconnect() {
	if a.sup != nil {
		a.sup.Close()
	}
}

// quitAndStop stops ytmd, and the music with it, then quits. The
// Supervisor goes first, or it would start ytmd again; a ytmd not running
// needs no stopping.
func (a *app) quitAndStop() {
	if a.daemon.stopping.IsRunning() {
		return
	}
	a.disconnect()
	d := a.daemon.ytmd
	if d.dialer == nil {
		// No paths, so no ytmd of this app's to stop.
		a.daemon.quit()
		return
	}
	job.Run(&a.daemon.stopping, a.post, func(ctx context.Context) (struct{}, error) {
		ctx, cancel := context.WithTimeout(ctx, remote.RestartGrace)
		defer cancel()
		c, _, err := remote.Connect(ctx, d.dialer, false, api.RoleDesktop, d.version)
		if err != nil {
			return struct{}{}, err
		}
		defer c.Close()
		return struct{}{}, c.Shutdown(ctx)
	}, func(_ struct{}, err error) {
		if err != nil {
			log.Debug().Err(err).Msg("stopping ytmd")
		}
		a.daemon.quit()
	})
}
