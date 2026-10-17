package main

import (
	"context"
	"errors"
	"net"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/remote"
	"yt-music-player/locales"

	"github.com/rs/zerolog/log"
)

// How long commands wait for the daemon.
const (
	// callTimeout bounds a control, Status and SignOut, which the daemon
	// answers at once.
	callTimeout = 10 * time.Second
	// signInTimeout bounds SignIn, which asks YouTube Music.
	signInTimeout = time.Minute
	// stopTimeout bounds Shutdown and Handoff, which save first.
	stopTimeout = 10 * time.Second
	// startTimeout bounds `daemon restart`'s wait for its own ytmd,
	// startRetry each start of it.
	startTimeout = remote.HandoffWait + 10*time.Second
	startRetry   = time.Second
	// repairWait is how long a command waits, at its end, for the
	// autostart repair it started.
	repairWait = time.Second
)

// errNotRunning is a daemon that took no connection, for a command that
// never starts one.
var errNotRunning = errors.New("ytm: the daemon is not running")

// connect connects to the daemon, starting it first when spawn is set
// and none answers. A dial that fails without spawn is errNotRunning; a
// daemon that took the connection but did not greet is
// api.ErrDaemonUnavailable.
func (e *env) connect(ctx context.Context, spawn bool) (*remote.Client, remote.Peer, error) {
	c, peer, err := remote.Connect(ctx, e.dialer, spawn, api.RoleCLI, e.version)
	if err != nil && !spawn && ctx.Err() == nil && !errors.Is(err, api.ErrDaemonUnavailable) {
		log.Debug().Err(err).Msg("ytm: no daemon")
		err = errNotRunning
	}
	return c, peer, err
}

// open connects, starting ytmd if need be, for a command that works the
// daemon: it refuses one of another protocol (checkPeer). ok is false once
// a failure under what was told.
func (e *env) open(ctx context.Context, what string) (c *remote.Client, ok bool) {
	c, peer, err := e.connect(ctx, true)
	if err != nil {
		e.fail(what, err)
		return nil, false
	}
	if !e.checkPeer(peer) {
		c.Close()
		return nil, false
	}
	return c, true
}

// checkPeer tells of a version skew: a warning for an older daemon, an
// error for one of another protocol, when it returns false.
func (e *env) checkPeer(peer remote.Peer) bool {
	theirs := peer.Hello.Version
	switch {
	case !peer.IsCompatible && peer.IsNewer:
		writeLine(e.stderr, locales.CliDaemonNewerIncompatible(theirs, e.version))
		return false
	case !peer.IsCompatible:
		writeLine(e.stderr, locales.CliDaemonIncompatible(theirs, e.version))
		return false
	case peer.IsOlder:
		writeLine(e.stderr, locales.CliDaemonOlder(theirs, e.version))
	}
	return true
}

// fail tells that what failed for err and returns exitFailed.
func (e *env) fail(what string, err error) int {
	log.Debug().Err(err).Str("what", what).Msg("ytm: command failed")
	writeLine(e.stderr, locales.CliFailed(what, e.why(err)))
	return exitFailed
}

// why says err in the user's words. Only an unforeseen error is said as
// it is.
func (e *env) why(err error) string {
	switch {
	case errors.Is(err, errNotRunning):
		return locales.CliWhyNotRunning()
	case errors.Is(err, api.ErrDaemonNotFound):
		return locales.CliWhyNotFound()
	case errors.Is(err, api.ErrDaemonUnavailable):
		return locales.CliWhyUnavailable()
	case errors.Is(err, api.ErrDisconnected):
		return locales.CliWhyDisconnected()
	case errors.Is(err, context.DeadlineExceeded):
		return locales.CliWhyTimeout()
	case errors.Is(err, context.Canceled):
		return locales.CliWhyCanceled()
	case errors.Is(err, catalog.ErrSignedOut):
		return locales.CliWhySignedOut()
	case errors.Is(err, catalog.ErrNotFound):
		return locales.NotFoundTitle()
	case errors.Is(err, api.ErrInternal):
		return locales.CliWhyDaemonFailed(e.paths.Log("ytmd"))
	}
	return err.Error()
}

// waiting dials as Wait does: until the daemon answers or ctx ends.
type waiting struct{ remote.Dialer }

func (w waiting) Dial(ctx context.Context) (net.Conn, error) { return w.Wait(ctx) }
