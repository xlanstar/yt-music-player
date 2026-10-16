package remote

import "strconv"

// Health is the state of the connection to the daemon. It lasts, so a
// front-end shows it (a banner, the status bar) rather than telling it
// as a notice.
type Health uint8

// The healths.
const (
	// Connecting: the first connection is being made.
	Connecting Health = iota
	// Connected: calls go through.
	Connected
	// Reconnecting: the daemon went away; connecting again, starting it
	// if need be.
	Reconnecting
	// Unresponsive: connected, but a Ping got no reply in time; offer
	// Restart. Any frame received makes it Connected again.
	Unresponsive
	// Unavailable: several attempts in a row failed; still trying, more
	// and more slowly. Offer Retry and the log.
	Unavailable
	// DaemonOutdated: the daemon is older than this front-end, even after
	// a hand-off; ask the user to update. Calls still go through if its
	// Proto is this one's (Link.Daemon.IsCompatible).
	DaemonOutdated
	// FrontendOutdated: the daemon is newer and speaks another Proto; ask
	// the user to update this front-end. No call goes through.
	FrontendOutdated
)

var healthNames = [...]string{
	Connecting:       "Connecting",
	Connected:        "Connected",
	Reconnecting:     "Reconnecting",
	Unresponsive:     "Unresponsive",
	Unavailable:      "Unavailable",
	DaemonOutdated:   "DaemonOutdated",
	FrontendOutdated: "FrontendOutdated",
}

func (h Health) String() string {
	if int(h) < len(healthNames) {
		return healthNames[h]
	}
	return "Health(" + strconv.Itoa(int(h)) + ")"
}

// Link is the connection to the daemon as a front-end shows it.
type Link struct {
	Health Health
	// Daemon is the daemon last greeted; its zero value before any.
	// DaemonOutdated shows Daemon.Hello.Version and Daemon.Version.
	Daemon Peer
	// Connection counts the connections made. Links are posted as they
	// are by then, so a quick reconnection may show no Reconnecting: a
	// new Connection tells that the daemon's state was sent anew, and that
	// what failed meanwhile may be fetched again.
	Connection uint64
}
