// Package remote is a front-end's side of ytmd: a Client over the
// daemon's socket and the Supervisor that keeps it connected.
//
// The Client fetches the pages' music and the session's library and
// likes, as a catalog.Source would; it is the front-end's Controller of
// playback, and the Mirror of the daemon's playback, which only the
// daemon's events change; Account asks ytmd's account over it. Its
// reader goroutine hands replies to callers and posts each burst of
// events once to the front-end's thread, where the Mirror and the Handler
// take them.
//
// The Supervisor checks the daemon only when something happens, never on
// a timer; its Link says how the connection is. A command connects once
// with Connect.
package remote
