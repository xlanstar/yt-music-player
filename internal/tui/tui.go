// Package tui is YT Music Player in the terminal, `ytm` without arguments
// (docs/daemon.md): a Bubble Tea program over ytmd, through
// a remote.Client that a Supervisor keeps connected. Three tabs (the
// queue, search, the library with its albums, playlists and liked songs)
// sit over the player bar, under which a line tells the daemon's health
// when it is not connected; there are no images.
//
// The mouse (cell motion, so no message per move): a click selects a row,
// a second click or a double click plays or opens it; the progress seeks
// where it is let go; the wheel moves the cursor. Render notes what it
// draws that a click acts on; a click looks it up (mouse.go).
//
// Threading: Bubble Tea's Update goroutine is the UI thread. The session,
// the loader, the catalog and the Mirror are touched there only. Their
// goroutines, and remote's, post funcs, which queue (post must not block,
// and tea.Program.Send does) until one message runs them all in Update.
//
// Redraws: the screen is rendered in Update, once per message that
// changed something, and View returns it. While a song plays in a
// focused terminal (one that never reports focus counts as focused), a
// tick a second moves the progress on; paused or blurred, nothing wakes.
// Bubble Tea's renderer still wakes at its frame rate to compare frames,
// writing nothing when they match: the TUI asks for few frames (fps).
//
// The session's fetches are not started here: ytmd sends the account,
// library and likes it has as soon as the TUI connects, and fetches them
// itself; a fetch that failed shows Retry.
//
// Every word comes from locales; every notice, the session's and remote's,
// goes through tell to the status line, said by noticetext.
package tui

import (
	"context"
	"errors"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/fetchstate"
	"yt-music-player/internal/loader"
	"yt-music-player/internal/remote"
	"yt-music-player/internal/session"

	tea "charm.land/bubbletea/v2"
)

// fps is how often Bubble Tea's renderer looks for a new frame. A key's
// answer shows within a frame; the progress moves once a second.
const fps = 30

// Config is what the TUI needs from ytm.
type Config struct {
	// Dialer reaches ytmd, starting it if need be.
	Dialer remote.Dialer
	// Spawn starts ytm's own ytmd after a hand-off; nil is
	// ipc.SpawnDaemon.
	Spawn func() error
	// Version is ytm's.
	Version string
	// DaemonLog is the path of ytmd's log, shown when it cannot be
	// reached.
	DaemonLog string
}

// Run runs the TUI until the user quits or ctx ends. Quitting leaves ytmd
// playing.
func Run(ctx context.Context, cfg Config) error {
	a := newApp(cfg) //nolint:contextcheck // the fetches end with the app (loader.Stop), not with ctx
	p := tea.NewProgram(a.m, tea.WithContext(ctx), tea.WithFPS(fps))
	pumpCtx, stop := context.WithCancel(ctx)
	go a.posts.pump(pumpCtx, p.Send)
	_, err := p.Run()
	stop()
	// Update is done: this goroutine is the UI thread now.
	a.close()
	switch {
	case errors.Is(err, tea.ErrInterrupted), errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil:
		// A signal ended it: a quit, which leaves ytmd playing.
		return nil
	}
	return err
}

// app is the model and what feeds it.
type app struct {
	m     *model
	posts *poster
	sup   *remote.Supervisor
}

// newApp wires a Client, under a Supervisor, to a session, a loader and
// the model, and starts connecting.
func newApp(cfg Config) *app {
	posts := newPoster()
	m := newModel(deps{})
	c := remote.NewClient(posts.post, remote.Handler{
		Account: func(e api.AccountEvent) { m.session.ApplyAccount(e) },
		Library: func(l catalog.Library) { m.session.ApplyLibrary(l) },
		Likes:   func(l api.Likes) { m.session.ApplyLikes(l) },
		Failed:  func(e api.FailedEvent) { m.session.ApplyFailed(e) },
		Notice:  m.tell,
		Link:    m.setLink,
	})
	states := &fetchstate.Registry{}
	cat := catalog.New(nil, nil, nil)
	load := loader.New(c, states, cat, posts.post)
	m.deps = deps{
		ctl:         c,
		mirror:      c.Mirror(),
		session:     session.New(session.NewRemote(c), states, load, cat),
		loader:      load,
		cat:         cat,
		states:      states,
		posts:       posts,
		now:         time.Now,
		openDesktop: openDesktop,
		daemonLog:   cfg.DaemonLog,
	}
	sup := remote.Supervise(c, remote.Options{
		Role: api.RoleTUI, Version: cfg.Version, Dialer: cfg.Dialer, Spawn: cfg.Spawn,
	})
	m.sup = sup
	return &app{m: m, posts: posts, sup: sup}
}

// close stops the fetches and disconnects, on the UI thread.
func (a *app) close() {
	a.m.loader.Stop()
	a.sup.Close()
}
