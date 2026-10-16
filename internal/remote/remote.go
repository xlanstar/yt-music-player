// Package remote is a front-end's side of ytmd (Desktop's, the TUI's, the
// CLI's): a Client over the daemon's socket, and the Supervisor that keeps
// it connected.
//
// The Client is the front-end's catalog.Source and catalog.Accounts, so
// its loader, session and sign-in work as they did in process; a
// Controller, whose controls the UI thread sends without waiting; and the
// Mirror of the daemon's playback, which only the daemon's events change.
//
// Threading: each connection has one reader goroutine. It hands replies to
// their callers, and gathers events, which reach the front-end's thread
// in one post per burst: the Mirror and the Handler's funcs run there
// only. Goroutines here hand over plain values; nothing they touch
// belongs to the UI.
//
// A Supervisor checks the daemon only when something happens, never on a
// timer: on connecting (Hello, versions), on the connection's end
// (connect again, with backoff), when the front-end comes to the
// foreground or a control goes unanswered (a Ping). Its Link says how
// the connection is, for a banner or status bar; failures of calls are
// errors and notices, as before.
//
// A command (`ytm next`) connects once with Connect and never hands off.
package remote
