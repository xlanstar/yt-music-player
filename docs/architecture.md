# Architecture

Three programs: `ytmd` plays, the Desktop and `ytm` browse and control it. What each links and never links: [daemon.md](daemon.md#programs). Package docs hold the details (`go doc ./internal/<pkg>`).

## Overview

```
YouTube Music ── innertube ── ytmusic (catalog.Source)      stream ── audio (engine)
                                   │                                    │
                ytmd (daemon, on its loop): player, playback, account, saved state
                                   │  api frames over ipc, one socket
                ┌──────────────────┴──────────────────┐
     Desktop: remote → session, loader → cmd/desktop   ytm: remote → session, loader → tui
```

- ytmd owns playback and the account; front-ends mirror them and fetch pages through it ([daemon.md](daemon.md#protocol)).
- Threads: ytmd's loop never waits on a socket; a front-end's posted funcs run on its UI thread ([daemon.md](daemon.md#threading)).

## Music

- `catalog`: music types by stable string ID; `Source` (plus `Accounts`, for signing in), the UI-side `Catalog` cache (UI thread only, `Gen()`), the placeholder; `catalogtest`: a `Source` tests answer by hand.
- `innertube`: InnerTube transport; knows nothing about music.
- `ytmusic`: `catalog.Source` over InnerTube, after ytmusicapi. Song = videoId, Album = `MPREb_…`, Playlist = playlistId without `VL`. Responses decode into the structs of `response.go`. Tests are offline against `testdata/`.
- `auth`: browser-cookie credentials and their store (Keychain on macOS).
- `stream`: resolves a song's Opus stream; `webm`: an audio track's frames out of WebM.
- `audio`: the engine (fetches, decodes Opus in Go, plays through MyGo's `audio`; Now Playing on macOS, MPRIS on Linux) and `Fake`.
- `artwork`: thumbnail loader with memory and disk caches.
- `httpclient`: a process's HTTP client, tuned for YouTube's hosts, through the system's proxy (MyGo's `netproxy`).

## The background player

- `loop`: one goroutine's run queue, the daemon's `post`.
- `daemon`: ytmd's service (player, playback, account, engine and `daemon.json` on the loop; serves `api` over `ipc`).
- `player`: queue, shuffle, repeat, volume; `playback`: drives the engine from the player, on ytmd's loop.
- `account`: ytmd's sign-in/out, fetching who is signed in, sending likes; reports changes as plain values and holds no UI state, so a process without a window can own it.
- `api`: the protocol between ytmd and its front-ends (frames, methods, events, error codes), types and pure functions only.
- `ipc`: ytmd's paths, single-instance lock, listening, dialing and starting ytmd, and the frame stream whose sends never block.
- `version`: ytmd's and ytm's version (`-ldflags -X`, else `dev`); `logfile`: ytmd's and ytm's size-bounded log files.
- `autostart`: ytmd at login (LaunchAgent, systemd user unit, HKCU Run) and, on Windows, the app's folder on the Path.
- `quiet`: the heap back to the OS once builds (Desktop), Update (TUI) or the loop (ytmd) stop.

## Front-ends

- `remote`: a front-end's side of ytmd: `Client` (the pages' music for `loader`, the session's library and likes, controls, the mirror of playback), `Account` (what the session asks of ytmd's account, over the Client) and `Supervisor` (reconnects, versions, hand-off, the daemon's health).
- `session`: the account as pages read it (fetch states, the catalog, notices): who is signed in, from ytmd's `account` events, and the library and likes, which it fetches through ytmd and keeps.
- `session` asks ytmd's account through `remote.Account`; every like goes through `SetLiked`, shown at once and undone on failure.
- `accountcache`: the library and likes on disk, which a front-end's session shows at start while fetching them.
- `loader`: page fetches (album, playlist, feeds, search) for the page shown.
- `fetchstate`: `Resource` and `Fetch` state; the `Registry` pages read (`a.fetches`).
- `job`: one piece of off-thread work whose `done` runs on the UI thread, dropped when stale.
- `notice`: messages for the user, made by session and playback, as codes and arguments; `noticetext`: their words, which `tell` shows (`Render`).
- `duration`: lengths of time as the Desktop, the TUI and `ytm status` say them (`Clock`, `Minutes`); knows no UI.
- `theme`: design tokens, presets, derived palettes, theme files ([theme.md](theme.md)); `settings`: preferences and user themes on disk.

## Support

- `atomicfile`: files replaced whole (a temporary file renamed over them), for every file the programs replace.
- `budget`: allocation budgets for tests ([benchmarks.md](benchmarks.md#budgets)); `leakcheck`: fails a test binary that leaves goroutines blocked for good.

## The Desktop

`cmd/desktop` is `package main`, the MyGo app; `mygo.json`, `resources/` and `locales/` stay at the root, and `mygo.json`'s `main` names this package.

- `main.go` (the window, the menu bar, the updater plugin), `demo.go` (`demo_placeholder.go` under the `placeholder` tag starts with the placeholder's music), `post.go` (`a.post`, buffered until the window attaches).
- `network.go` (back online: ytmd and what failed are retried), `ytm_linux.go` (started as `ytm`, runs the `ytm` beside it).
- `daemon.go`: the remote client and its supervisor; routes notices to `tell`, the daemon's health to `banner.go` (Retry, Restart, View Log); Quit and Stop Playback.
- `startup.go`: starting ytmd at login and, on Windows, ytm on the Path (package `autostart`, through `job.Job.Run`).
- `app.go`: state, `reconcile` before each build, root layout from `th.Layout`. `memo.go`: `a.memo`, what builds derive, made again when its inputs change.
- `fetch.go` (router → resource, `syncLoad`), `account.go` + `signin.go` (the session over ytmd, which gets the account's events, `tell`: the one path for every notice).
- `playback.go`: what plays, from the mirror: the position's frames (timed here), the current song and those up next, controls, sliders held while dragged.
- `appearance.go`: the settings sheet.
- `routes.go`: `/home`, `/explore`, `/library?tab=`, `/album/{id}`, `/playlist/{id}`, `/liked`, `/search?q=`.
- Pages: `home.go`, `explore.go`, `library.go`, `collection.go`, `search.go`. Chrome: `sidebar.go`, `topbar.go`, `playerbar.go`, `nowplaying.go`.
- `rows.go` (the one song row), `widgets.go`, `theme.go` (`th`), `art.go`, `phrase.go` (messages of string arguments in one allocation).
- `icons.go` + `icons/`: Lucide SVGs, embedded; `mygo.json` ships them too.

## ytm and the TUI

- `cmd/ytm`: the TUI without arguments, else one command to ytmd (`ytm help`; `commands.go`: one table dispatches, lists and says them), run against a fake daemon in tests; `testdata/status.json` holds `status --json`.
- `tui`: the player in the terminal (Bubble Tea, Lip Gloss); `Update` is the UI thread, where posted funcs run as one message.
- A render is `prepare` (`frame.go`; `layout.go` places the parts by the rows each declares) → `draw` (`view.go`, `rows.go`, `scene.go`) → `covers.sync` → `compose`.
- `draw` puts each part in its `region`'s own cells, never the screen's; spacing is `padX`/`gapX`.
- A part's cover is a rect and an image, whose protocol, size key and overlay are `covers.go`'s; screens are golden files in `testdata/` ([testing.md](testing.md#golden-files)).
- `termimage`: images in a terminal (Kitty's Unicode placeholders, iTerm2, Sixel, half blocks): detection, encoders and the marks that splice overlays into frames, no I/O.
- The TUI's covers: `tui/probe.go` (detection), `tui/covers.go` (loads, encodes, the overlays' tables), `tui/termout.go` (the terminal Bubble Tea writes to).

## ytmd

`cmd/ytmd`: the daemon's `main` (lock, state, socket, run loop, signals); `source.go` (`source_placeholder.go` under the tag: the fake, playing silently on a clock).
