package remote

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/ipc"
	"yt-music-player/internal/version"
)

// helloTimeout is how long a daemon that took the connection has to
// greet.
const helloTimeout = 5 * time.Second

var errNoHello = fmt.Errorf("%w: the daemon did not greet first", api.ErrInvalid)

// Dialer connects to the daemon's socket: an *ipc.Dialer, or tests' pipes.
type Dialer interface {
	// Dial connects once, never starting ytmd.
	Dial(ctx context.Context) (net.Conn, error)
	// Wait dials until it connects or its timeout passes, never starting
	// ytmd.
	Wait(ctx context.Context) (net.Conn, error)
	// DialOrSpawn connects, or else starts ytmd and Waits for it.
	DialOrSpawn(ctx context.Context) (net.Conn, error)
}

var _ Dialer = (*ipc.Dialer)(nil)

// Peer is the daemon as it greeted, compared with this front-end.
type Peer struct {
	// Hello is the daemon's.
	Hello api.Hello
	// Version is this front-end's.
	Version string
	// IsCompatible reports the same Proto: otherwise only Ping, Handoff
	// and Shutdown work.
	IsCompatible bool
	// IsOlder and IsNewer compare the daemon's version with Version;
	// neither holds for a development build.
	IsOlder, IsNewer bool
}

func compare(h api.Hello, ours string) Peer {
	return Peer{
		Hello:        h,
		Version:      ours,
		IsCompatible: h.Proto == api.Proto,
		IsOlder:      version.Older(h.Version, ours),
		IsNewer:      version.Older(ours, h.Version),
	}
}

// greet sends ours and returns the daemon's Hello, its first frame; conn
// is closed on failure.
func greet(ctx context.Context, conn *ipc.Conn, ours api.Hello) (api.Hello, error) {
	ctx, cancel := context.WithTimeout(ctx, helloTimeout)
	defer cancel()
	// Unblocks Receive.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if err := conn.Send(api.Frame{Hello: &ours}); err != nil {
		_ = conn.Close()
		return api.Hello{}, err
	}
	var f api.Frame
	err := conn.Receive(&f)
	switch {
	case ctx.Err() != nil:
		err = ctx.Err()
	case err != nil:
	case f.Hello == nil:
		err = errNoHello
	default:
		err = f.Hello.Validate()
	}
	if err != nil {
		_ = conn.Close()
		return api.Hello{}, err
	}
	return *f.Hello, nil
}

func hello(role api.Role, ours string, subscribe bool) api.Hello {
	return api.Hello{Proto: api.Proto, Version: ours, Role: role, Subscribe: subscribe, PID: os.Getpid()}
}

// Connect connects a Client once, for a command, starting ytmd if spawn
// is set and none answers. It never subscribes nor hands off: the caller
// tells of a version skew, and calls only Ping, Handoff and Shutdown
// unless the Peer IsCompatible. Close the Client when done.
func Connect(ctx context.Context, d Dialer, spawn bool, role api.Role, ours string) (*Client, Peer, error) {
	dial := d.Dial
	if spawn {
		dial = d.DialOrSpawn
	}
	nc, err := dial(ctx)
	if err != nil {
		return nil, Peer{}, err
	}
	conn := ipc.NewConn(nc)
	h, err := greet(ctx, conn, hello(role, ours, false))
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, Peer{}, err
		}
		return nil, Peer{}, fmt.Errorf("%w: %w", api.ErrDaemonUnavailable, err)
	}
	peer := compare(h, ours)
	// No events come: callbacks run where posted.
	c := NewClient(func(fn func()) { fn() }, Handler{})
	c.open(conn, peer, false)
	return c, peer, nil
}
