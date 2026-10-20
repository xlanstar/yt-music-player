# YT Music Player

An efficient, lightweight YouTube Music desktop player with a modern UI, written in Go 1.27 with MyGo native UI (GPU-drawn, no webview). Songs play in a background player, `ytmd`, which the Desktop and `ytm` (terminal player and command line) control.

MyGo docs: https://mygo.egoist.dev/docs.md (always read the `.md` pages, never `.html`).

## Commands

- `prek`: the only check needed; it must pass.
- `go run ./cmd/ytmd`, then `go run ./cmd/desktop` in another terminal: the Desktop on YouTube Music, signed in from ytmd's Keychain item, playing through the Go engine (`internal/audio`). Front-ends use the ytmd running, else start the one at `YTMD_PATH` or beside themselves; with none, the Desktop shows the banner.
- `-tags placeholder` on both: the offline fake, always signed in, playing silently on a clock; `YT_SOURCE_DELAY=800ms` on its ytmd shows loading states; release builds don't link it. ytmd outlives the front-end, and one already running serves the next run, old code and all: Ctrl+C, `ytm daemon stop` or the app menu's Quit and Stop Playback stops it.
- `YT_LOG=debug|info|warn|error` (default `warn`): `debug` tells why fetches, likes, sign-ins and streams failed.
- ytmd: `docs/daemon.md`. Development builds use their own `-dev` socket, state and log. To check ytmd from commands, without a window: `go build -o build/dev/ ./cmd/ytmd ./cmd/ytm` once (`-tags placeholder -o build/dev-placeholder/` for the fake), then `build/dev/ytm …` (`help` lists them; none: the TUI), which starts the ytmd beside it; never mix the two directories.
- Live checks (never in CI): `go test -run Record -update -v ./internal/ytmusic/` (re-records `testdata/`, `Record/catalog` or `Record/feeds` for one half; `YTM_COOKIE=…` with `Record/library` for signed-in, writes nothing); `go test ./internal/stream/ -run Live -live -v`; `go test -tags audiolive -run Live -v ./internal/audio/` and `go test -tags audiolive -run LiveDaemon -v ./internal/daemon/` (play sound); `scripts/footprint.sh -daemon` (below); `TestMPRIS` runs where there is a session bus (`dbus-run-session -- go test -run MPRIS ./internal/audio/`); `YTM_KEYCHAIN_TEST=1` for the Keychain.
- `locales/`: the messages, `en.json` (the source, with descriptions) and `zh-Hant.json`, in ICU MessageFormat; `go tool mygo generate .` writes `locales/catalog_gen.go` (committed) from them, which `prek` checks.
- `scripts/render-app-icon.sh`: renders the icon's one source, `assets/icon/icon.svg`, into `resources/icon.png` and `assets/icon/` (PNGs of every size, `.icns`, `.ico`); edit the SVG, never the renders.
- `scripts/repo-images.sh [-readme]`: renders the social preview (uploaded in the repository's settings, not committed) and README's top image from `assets/repo-images/` and captures of the placeholder build and its `ytm` in kitty; `-readme` updates `docs/images/`. Run it with `-readme` when the window's look changes.
- Release: `scripts/release.sh patch|minor|major`, then, after checking the draft, approve the Release workflow's `publish` job, which publishes it and updates the Homebrew tap (`docs/release.md`); the version lives only in `mygo.json`, user-visible changes go under `## [Unreleased]` in `CHANGELOG.md` in the same change. The cask is `packaging/homebrew/yt-music-player.rb`; never edit it in the tap.
- Build the app only with `scripts/build.sh [mygo build flags]`, never a bare `go tool mygo build`, which ships old ytmd and ytm or none. To use a build on macOS: `scripts/install.sh [-universal]` installs it through the release's cask from a local Homebrew tap.
- Performance: `docs/performance.md`; benchmarks and budgets: `docs/benchmarks.md`; memory and frames of the real window: `scripts/footprint.sh` (macOS, about 70 seconds); `-daemon`: ytmd's memory and wake-ups (a paused ytmd must wake nothing); the release binaries' sizes: `scripts/binsize.sh` (about 10 seconds cached, `-against <rev>`). Each compares with its committed `perf/*-baseline.txt`; a run writes `perf/*-latest.txt` (gitignored).

## Layout

Details live in each package's doc comment (`go doc ./internal/<pkg>`).

`internal/`:

- `catalog`: music types by stable string ID; `Source` (plus `Accounts`, for signing in), the UI-side `Catalog` cache (UI thread only, `Gen()`), the placeholder; `catalogtest`: a `Source` tests answer by hand.
- `job`: one piece of off-thread work whose `done` runs on the UI thread, dropped when stale.
- `fetchstate`: `Resource` and `Fetch` state; the `Registry` pages read (`a.fetches`).
- `notice`: messages for the user, made by session and playback, as codes and arguments; `noticetext`: their words, which `tell` shows (`Render`).
- `loader`: page fetches (album, playlist, feeds, search) for the page shown.
- `account`: sign-in/out, fetching the account, library and likes, sending likes; reports changes as plain values and holds no UI state, so a process without a window can own it.
- `session`: what ytmd's `account` reports (its events), as pages read it (fetch states, the catalog, notices); asks ytmd through the remote client; every like through `SetLiked`, shown at once and undone on failure.
- `auth`: browser-cookie credentials and their store (Keychain on macOS).
- `innertube`: InnerTube transport; knows nothing about music.
- `ytmusic`: `catalog.Source` over InnerTube, after ytmusicapi. Song = videoId, Album = `MPREb_…`, Playlist = playlistId without `VL`. Responses decode into the structs of `response.go`. Tests are offline against `testdata/`.
- `stream`: resolves a song's Opus stream; `audio`: the engine (fetches, decodes Opus in Go, plays through MyGo's `audio`; Now Playing on macOS, MPRIS on Linux) and `Fake`; `webm`: an audio track's frames out of WebM.
- `player`: queue, shuffle, repeat, volume; `playback`: drives the engine from the player, on ytmd's loop; the Desktop times the position's frames itself (`cmd/desktop/playback.go`).
- `api`: the protocol between ytmd and its front-ends (frames, methods, events, error codes), types and pure functions only; `ipc`: ytmd's paths, single-instance lock, listening, dialing and starting ytmd, and the frame stream whose sends never block.
- `remote`: a front-end's side of ytmd: `Client` (the front-end's `catalog.Source`, controls, the mirror of playback) and `Supervisor` (reconnects, versions, hand-off, the daemon's health).
- `version`: ytmd's and ytm's version (`-ldflags -X`, else `dev`); `logfile`: ytmd's and ytm's size-bounded log files; `autostart`: ytmd at login (LaunchAgent, systemd user unit, HKCU Run) and, on Windows, the app's folder on the Path.
- `quiet`: the heap back to the OS once builds (Desktop), Update (TUI) or the loop (ytmd) stop.
- `loop`: one goroutine's run queue, the daemon's `post`; `daemon`: ytmd's service (player, playback, account, engine and `daemon.json` on the loop; serves `api` over `ipc`).
- `tui`: `ytm`'s player in the terminal (Bubble Tea, Lip Gloss); `Update` is the UI thread, where posted funcs run as one message; screens are golden files in `testdata/` (`-update` rewrites them).
- `termimage`: images in a terminal (Kitty's Unicode placeholders, iTerm2, Sixel, half blocks): detection, encoders and the marks that splice overlays into frames, no I/O; the TUI's covers (`tui/graphics.go`, `tui/termout.go`: the terminal Bubble Tea writes to).
- `theme`: design tokens, presets, derived palettes, theme files (`docs/theme.md`); `settings`: preferences and user themes on disk.
- `artwork`: thumbnail loader with memory and disk caches.
- `httpclient`: a process's HTTP client, tuned for YouTube's hosts.
- `budget`: allocation budgets for tests.

`cmd/desktop` (`package main`, the MyGo app; `mygo.json`, `resources/` and `locales/` stay at the root, `mygo.json`'s `main` names this package):

- `main.go` (the window, the menu bar, the updater plugin), `demo.go` (`demo_placeholder.go` under the `placeholder` tag starts with the placeholder's music), `post.go` (`a.post`, buffered until the window attaches), `network.go` (back online: ytmd and what failed are retried), `ytm_linux.go` (started as `ytm`, runs the `ytm` beside it).
- `daemon.go`: the remote client and its supervisor; routes the account's events to the session, notices to `tell`, the daemon's health to `banner.go` (Retry, Restart, View Log); Quit and Stop Playback. `startup.go`: starting ytmd at login and, on Windows, ytm on the Path (package `autostart`, through `job.Run`).
- `app.go`: state, `reconcile` before each build, root layout from `th.Layout`. `memo.go`: `a.memo`, what builds derive, made again when its inputs change.
- `fetch.go` (router → resource, `syncLoad`), `account.go` + `signin.go` (the session over ytmd, `tell`: the one path for every notice), `playback.go` (what plays, from the mirror: the position's frames, the current song and those up next, controls, sliders held while dragged), `appearance.go` (the settings sheet).
- `routes.go`: `/home`, `/explore`, `/library?tab=`, `/album/{id}`, `/playlist/{id}`, `/liked`, `/search?q=`.
- Pages: `home.go`, `explore.go`, `library.go`, `collection.go`, `search.go`. Chrome: `sidebar.go`, `topbar.go`, `playerbar.go`, `nowplaying.go`.
- `rows.go` (the one song row), `widgets.go`, `theme.go` (`th`), `art.go`, `icons.go` + `icons/` (Lucide SVGs, embedded; `mygo.json` ships them too), `phrase.go` (messages of string arguments in one allocation).

`cmd/ytmd`: the daemon's `main` (lock, state, socket, run loop, signals); `source.go` (`source_placeholder.go` under the tag: the fake, playing silently on a clock).

`cmd/ytm`: `ytm`, the TUI (`tui`) without arguments, else one command to ytmd (`ytm help`), run against a fake daemon in tests; `testdata/status.json` holds `status --json`.

What each binary must not link: `docs/daemon.md` (Programs), held by `TestDeps`.

## Rules

- Read the relevant docs and skills before changing code.
- `.agents/skills/golang-*` come from [samber/cc-skills-golang](https://github.com/samber/cc-skills-golang) (MIT, `evals/` and `golang-documentation` dropped). Where one disagrees with this file, this file wins.
- Comments: one or two lines, the deciding why. Docs: update in the same change, in place, never appended. Measurements and history go in the commit body.
- Follow Go and MyGo best practices. Build UI with MyGo's Native UI API, not its web frontend (no HTML/CSS/JS).
- Commits: [Conventional Commits](https://www.conventionalcommits.org) (`feat(player): add shuffle`), imperative, under 72 characters.

### Threading

- ytmd's player, playback, account and engine live on its loop (`loop.Post`), which never waits on a socket; the Desktop and TUI only mirror them.
- Never call a `Source` or touch `a.cat` from a goroutine: fetch off-thread with `job.Run`, whose `done` runs on the UI thread through `post`, dropped when stale, and merges.
- Goroutines return plain values; loader, account, playback, artwork and settings change shared state only in posted funcs.

### UI

- Read every color, font size, spacing and radius from `th`, never literals (circles' and skeletons' geometry aside).
- Pages read `a.fetches.State`; failures show Retry, `Missing` shows `notFound`. Loading states are static (no shimmer).
- Every `notice.Notice` goes through `tell`.
- Every word the user reads comes from `locales` (`locales.Play()`), in both catalogs; names from YouTube and keys stay as they are. Text kept between builds is dropped when the language changes (`syncLanguage`); a message of string arguments said often is a `phrase`.
- Icons only from Lucide, under Lucide's names: `scripts/fetch-lucide-icons.sh <name>…` fetches and compresses them; a filled one is `mustFilledIcon`, never drawn by hand.
- Long lists are one `ui.List` with the header as row 0.

### Performance

Reasons and measurements: `docs/performance.md`.

- Idle means no frames: no endless animations; animations while playing stop when unfocused or hidden.
- Round images with `ui.Image(...).Radius()`.
- Hover motion is an `AnimatePose`, whose frames only paint, where nothing inside clips: no `Transform`/`Scale`/`AnimatePose` on, or inside a rounded `Clip` around, anything that clips (painted covers but dots and stripes: `cover.clips`).
- Key elements by string or int; box keys used every build once.
- Don't remake per build what changes rarely: memoize it in `a.memo`; never copy a playlist's songs per build.
- JSON keeps encoding/json/v2's defaults; an option only for a need a test or file format shows.
- Run `scripts/bench.sh` before and after performance-sensitive changes (`-against <rev>` does both).
- Analyze binary size with `gsa` first: text or filtered JSON, not full symbol dumps or custom parsers ([usage](docs/benchmarks.md#attribution-with-gsa)).

### Logging

- At debug, only on failure paths, with typed fields (`Str`, `Err`, `Int`, `Stringer`), never strings built before the level check.
- Log IDs and errors; never credentials, cookies, headers, visitorData or stream URLs.
- A package that logs sets `zerolog.Disabled` in its `TestMain`.

### Tests

- No window, no network, no disk cache, no config dir.
- Apps sit on ytmd in the test's process (`ytmd_test.go`: a `daemon.Service` over `net.Pipe`, the placeholder's queue, no saved state): `testApp(t)`/`sourceApp(t, src)`, `idleApp(t)` (feeds preloaded), `scriptedApp(t)` (only opened pages ask), `anonApp(t)`, `accountApp(t)` (over `catalogtest.Accounts`), `playbackApp(t, res)` (ytmd plays on `audio.Fake`), `lookApp(t, prepare)` (settings in a `TempDir`). Tests show English; `inLanguage(t, "zh-Hant")` switches.
- `newScreen`/`showApp`/`newTester` show one on a Tester whose builds first run the Client's posts; `settleLoads`/`busy` wait for fetches, thumbnails, the session and ytmd's round trips (`syncDaemon`, `settled`); `answer` waits until ytmd took the reply and the app has it; scripted sources wait drawing frames: `src.Next(t, tt.Frame, …)`, `Take`, `None`.
- Loader, account, session, playback, job and settings logic is tested in their packages.

### Benchmarks and budgets

Details: `docs/benchmarks.md`.

- Each package has `bench_test.go` (`Benchmark<Function>/<case>`, deterministic inputs, one scaled case) and `budget_test.go` (`TestBudget<Function>`).
- Budgets hold allocations at about 1.15× today's count (0–2 exactly), with a `// N allocs today.` comment. Raising one needs its reason in the same change.
- A new `pageCases`/`interactionCases` case needs its budget.
