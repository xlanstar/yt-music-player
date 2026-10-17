package remote

import "strconv"

// Health is the state of the connection to the daemon, shown (a banner,
// the status bar) rather than told as a notice.
type Health uint8

// The healths (docs/daemon.md, Health).
const (
	Connecting Health = iota
	Connected
	Reconnecting
	// Unresponsive: a Ping got no reply in time; offer Restart.
	Unresponsive
	// Unavailable: several attempts in a row failed; offer Retry and the
	// log.
	Unavailable
	// DaemonOutdated: older even after a hand-off; calls still go
	// through if Link.Daemon.IsCompatible.
	DaemonOutdated
	// FrontendOutdated: the daemon is newer and speaks another Proto.
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
	// Daemon is the daemon last greeted, zero before any.
	Daemon Peer
	// Connection counts the connections made. A quick reconnection may
	// show no Reconnecting: a new Connection tells that the daemon's state
	// was sent anew, and that what failed meanwhile may be fetched again.
	Connection uint64
}
