// Package tui is YT Music Player in the terminal, `ytm` without arguments:
// a Bubble Tea program over ytmd (docs/daemon.md), through a
// remote.Client that a Supervisor keeps connected. Three tabs (the queue,
// search, the library) sit over the player bar.
//
// Covers show in the player bar and over an album or playlist where the
// terminal shows images (package termimage, gfx): Kitty's placeholders and
// half blocks are text. Sixel and iTerm2 images go over blank cells, which
// carry marks: termOut, the terminal Bubble Tea writes to, draws an image
// right after each frame that wrote its marks, so after each that wiped it.
//
// Update is the UI thread: the session, the loader, the catalog and the
// Mirror are touched there only. Other goroutines post funcs, which queue
// until one message runs them all, as tea.Program.Send blocks and post
// must not. termOut runs on Bubble Tea's renderer, reading the overlays'
// tables Update publishes.
//
// Update renders once per message that changed something: prepare lays
// the screen out, makes the page, scrolls its list and readies the
// covers into a frame, which draw only reads into the screen View
// returns. Keys and clicks read the model afresh, never a frame. Only a
// song playing in a focused terminal ticks, once a second.
//
// Lines measure text as Bubble Tea's renderer does (cells): by rune, by
// grapheme once the terminal says it knows mode 2027, which renders every
// line again.
package tui

import (
	"context"
	"errors"
	"io"
	"os"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/artwork"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/fetchstate"
	"yt-music-player/internal/loader"
	"yt-music-player/internal/remote"
	"yt-music-player/internal/session"
	"yt-music-player/internal/termimage"

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
	out := newTermOut(os.Stdout)
	a := newApp(cfg, out) //nolint:contextcheck // the fetches end with the app (loader.Stop), not with ctx
	p := tea.NewProgram(a.m, append(programOptions(out), tea.WithContext(ctx))...)
	pumpCtx, stop := context.WithCancel(ctx)
	go a.posts.pump(pumpCtx, p.Send)
	a.m.quiet.Start()
	_, err := p.Run()
	stop()
	// Update is done: this goroutine is the UI thread now.
	if images := a.close(); images != "" {
		// Bubble Tea, stopped, writes nothing queued: Kitty keeps images
		// until told.
		_, _ = io.WriteString(out, images)
	}
	switch {
	case errors.Is(err, tea.ErrInterrupted), errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil:
		// A signal ended it: a quit, which leaves ytmd playing.
		return nil
	}
	return err
}

// programOptions are the Program's, out its terminal.
func programOptions(out *termOut) []tea.ProgramOption {
	return []tea.ProgramOption{tea.WithFPS(fps), tea.WithOutput(out), tea.WithFilter(out.filter)}
}

// app is the model and what feeds it.
type app struct {
	m     *model
	posts *poster
	sup   *remote.Supervisor
}

// newApp wires a Client, under a Supervisor, to a session, a loader and
// the model, which draws on out, and starts connecting.
func newApp(cfg Config, out *termOut) *app {
	posts := newPoster()
	m := newModel(deps{})
	m.gfx = gfx{env: termimage.EnvFrom(os.Getenv), kittyBase: newKittyBase(), post: posts.post, out: out, newArt: func() *artwork.Loader {
		dir, _ := artwork.DefaultDir()
		return artwork.New(artwork.Options{Post: posts.post, Dir: dir, MemoryBytes: artMemory, Workers: artWorkers})
	}}
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

// close stops the fetches and disconnects, on the UI thread; it returns
// what frees the terminal's images.
func (a *app) close() string {
	a.m.loader.Stop()
	a.sup.Close()
	a.m.quiet.Stop()
	return a.m.gfx.close()
}
