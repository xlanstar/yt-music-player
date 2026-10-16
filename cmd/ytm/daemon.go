package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"yt-music-player/internal/api"
	"yt-music-player/internal/remote"
	"yt-music-player/locales"

	"github.com/rs/zerolog/log"
)

// daemonCommand is `ytm daemon start|stop|restart|status`.
func (e *env) daemonCommand(ctx context.Context, args []string) int {
	if len(args) != 1 {
		return e.usage("daemon")
	}
	switch args[0] {
	case "start":
		return e.daemonStart(ctx)
	case "stop":
		return e.daemonStop(ctx)
	case "restart":
		return e.daemonRestart(ctx)
	case "status":
		return e.daemonStatus(ctx)
	}
	return e.usage("daemon")
}

// daemonStart starts ytmd unless one runs, and waits until it answers.
func (e *env) daemonStart(ctx context.Context) int {
	c, peer, err := e.connect(ctx, true)
	if err != nil {
		return e.fail(locales.CliStartFailed(), err)
	}
	c.Close()
	e.checkPeer(peer)
	e.tellRunning(peer)
	return exitOK
}

// daemonStatus says whether ytmd runs; exitStopped when not.
func (e *env) daemonStatus(ctx context.Context) int {
	c, peer, err := e.connect(ctx, false)
	switch {
	case errors.Is(err, errNotRunning):
		writeLine(e.stdout, locales.CliDaemonNotRunning())
		return exitStopped
	case err != nil:
		return e.fail(locales.CliStatusFailed(), err)
	}
	c.Close()
	e.checkPeer(peer)
	e.tellRunning(peer)
	return exitOK
}

func (e *env) tellRunning(peer remote.Peer) {
	writeLine(e.stdout, locales.CliDaemonRunning(peer.Hello.Version, strconv.Itoa(peer.Hello.PID)))
}

// daemonStop stops ytmd, and playback; none running is success. Shutdown
// is a bootstrap call, so a daemon of any protocol stops.
func (e *env) daemonStop(ctx context.Context) int {
	what := locales.CliStopFailed()
	c, _, err := e.connect(ctx, false)
	switch {
	case errors.Is(err, errNotRunning):
		writeLine(e.stdout, locales.CliDaemonNotRunning())
		return exitOK
	case err != nil:
		return e.fail(what, err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(ctx, stopTimeout)
	defer cancel()
	if err := c.Shutdown(ctx); err != nil {
		return e.fail(what, err)
	}
	writeLine(e.stdout, locales.CliDaemonStopped())
	return exitOK
}

// daemonRestart hands the daemon off (docs/daemon.md, Versions and hand-off): it
// saves and exits, telling the other front-ends to wait, and this ytm
// starts its own ytmd, which plays on if the old one played. A daemon of
// any protocol hands off. With none running, it starts one.
func (e *env) daemonRestart(ctx context.Context) int {
	what := locales.CliRestartFailed()
	c, peer, err := e.connect(ctx, false)
	switch {
	case errors.Is(err, errNotRunning):
		return e.daemonStart(ctx)
	case err != nil:
		return e.fail(what, err)
	}
	handoff, cancel := context.WithTimeout(ctx, stopTimeout)
	err = c.Handoff(handoff, remote.HandoffWait)
	cancel()
	c.Close()
	if err != nil {
		return e.fail(what, err)
	}
	c, peer, err = e.startOwn(ctx, peer.Hello.PID)
	if err != nil {
		return e.fail(what, err)
	}
	c.Close()
	e.checkPeer(peer)
	e.tellRunning(peer)
	return exitOK
}

// startOwn starts this ytm's own ytmd and waits until it greets. It starts
// it again every startRetry: one started while the daemon handing off
// still holds the lock exits at once. Whatever still answers as the
// daemon of oldPID is let be until it goes.
func (e *env) startOwn(ctx context.Context, oldPID int) (*remote.Client, remote.Peer, error) {
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	for {
		if err := e.spawn(); err != nil {
			return nil, remote.Peer{}, err
		}
		attempt, stop := context.WithTimeout(ctx, startRetry)
		c, peer, err := remote.Connect(attempt, waiting{e.dialer}, false, api.RoleCLI, e.version)
		if err == nil && peer.Hello.PID == oldPID {
			c.Close()
			// Waits out the attempt rather than dial it again at once.
			<-attempt.Done()
			err = api.ErrDaemonUnavailable
		}
		stop()
		switch {
		case err == nil:
			return c, peer, nil
		case ctx.Err() != nil:
			return nil, remote.Peer{}, fmt.Errorf("%w: %w", api.ErrDaemonUnavailable, err)
		}
		log.Debug().Err(err).Msg("ytm: the new daemon has not answered yet")
	}
}

// versionCommand says the versions and protocols of ytm and of the
// daemon running, if any.
func (e *env) versionCommand(ctx context.Context) int {
	writeLine(e.stdout, "ytm "+e.version+" (proto "+strconv.Itoa(int(api.Proto))+")")
	c, peer, err := e.connect(ctx, false)
	switch {
	case errors.Is(err, errNotRunning):
		return exitOK
	case err != nil:
		return e.fail(locales.CliStatusFailed(), err)
	}
	c.Close()
	writeLine(e.stdout, "ytmd "+peer.Hello.Version+" (proto "+strconv.Itoa(int(peer.Hello.Proto))+")")
	e.checkPeer(peer)
	return exitOK
}

// logs prints the paths of the log files, the daemon's first.
func (e *env) logs() int {
	writeLine(e.stdout, e.paths.Log("ytmd"))
	writeLine(e.stdout, e.paths.Log("ytm"))
	return exitOK
}
