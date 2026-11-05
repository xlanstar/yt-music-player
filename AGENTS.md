# YT Music Player

An efficient, lightweight YouTube Music desktop player with a modern UI, written in Go 1.27 with MyGo native UI (GPU-drawn, no webview). Songs play in a background player, `ytmd`, which the Desktop and `ytm` (terminal player and command line) control.

MyGo docs: https://mygo.egoist.dev/docs.md (always read the `.md` pages, never `.html`).

## Commands

- `prek`: the only check needed; it must pass. It also runs `scripts/check-docs.sh` (links, anchors, this file's Documentation list) and `scripts/check-prose.sh` (short comments and docs).
- `go run ./cmd/ytmd`, then `go run ./cmd/desktop` in another terminal: the Desktop on YouTube Music, signed in from ytmd's Keychain item, playing through the Go engine (`internal/audio`). Front-ends use the ytmd running, else start the one at `YTMD_PATH` or beside themselves; with none, the Desktop shows the banner.
- `-tags placeholder` on both: the offline fake, always signed in, playing silently on a clock; `YT_SOURCE_DELAY=800ms` on its ytmd shows loading states; release builds don't link it. ytmd outlives the front-end, and one already running serves the next run, old code and all: Ctrl+C, `ytm daemon stop` or the app menu's Quit and Stop Playback stops it.
- `YT_LOG=debug|info|warn|error` (default `warn`): `debug` tells why fetches, likes, sign-ins and streams failed.
- ytmd without a window: `go build -o build/dev/ ./cmd/ytmd ./cmd/ytm` once (`-tags placeholder -o build/dev-placeholder/` for the fake), then `build/dev/ytm …` (`help` lists them; none: the TUI), which starts the ytmd beside it; never mix the two directories. Development builds use their own `-dev` socket, state and log.
- Live checks (never in CI): `go test -run Record -update -v ./internal/ytmusic/` (re-records `testdata/`, `Record/catalog` or `Record/feeds` for one half; `YTM_COOKIE=…` with `Record/library` for signed-in, writes nothing); `go test ./internal/stream/ -run Live -live -v`; `go test -tags audiolive -run Live -v ./internal/audio/` and `go test -tags audiolive -run LiveDaemon -v ./internal/daemon/` (play sound); `scripts/footprint.sh -daemon` (below); `TestMPRIS` runs where there is a session bus (`dbus-run-session -- go test -run MPRIS ./internal/audio/`); `YTM_KEYCHAIN_TEST=1` for the Keychain.
- `go tool mygo generate .`: writes `locales/catalog_gen.go` (committed) from `locales/en.json` (the source, with descriptions) and `zh-Hant.json`, in ICU MessageFormat; `prek` checks it.
- `scripts/fetch-lucide-icons.sh <name>…`: fetches and compresses Lucide icons into `cmd/desktop/icons/`.
- `scripts/render-app-icon.sh`: renders the icon's one source, `assets/icon/icon.svg`, into `resources/icon.png` and `assets/icon/` (PNGs of every size, `.icns`, `.ico`); edit the SVG, never the renders.
- `scripts/repo-images.sh [-readme]`: renders the social preview (uploaded in the repository's settings, not committed) and README's top image from `assets/repo-images/` and captures of the placeholder build and its `ytm` in kitty; `-readme` updates `docs/images/`. Run it with `-readme` when the window's look changes.
- `scripts/build.sh [mygo build flags]`: the only way to build the app; a bare `go tool mygo build` ships old ytmd and ytm or none. `scripts/install.sh [-universal]` installs a build on macOS through the release's cask from a local Homebrew tap.
- `scripts/release.sh patch|minor|major` (`patch` on a branch `X.Y` patches an older version), then, after checking the draft, approve the Release workflow's `publish` job, which publishes it and updates the Homebrew tap (`docs/release.md`). The version lives only in `mygo.json`. The cask is `packaging/homebrew/yt-music-player.rb`; never edit it in the tap.

### Performance

Why and how: `docs/performance.md`; benchmarks and budgets: `docs/benchmarks.md`. Each compares with its committed `perf/*-baseline.txt`; a run writes `perf/*-latest.txt` (gitignored).

```sh
scripts/bench.sh [-against <rev>]    # benchmarks; before and after performance-sensitive changes
scripts/footprint.sh [-live]         # memory and frames of the real window (macOS, about 70 s)
scripts/footprint.sh -daemon         # ytmd's memory and wake-ups; a paused ytmd must wake nothing
scripts/binsize.sh [-against <rev>]  # the release binaries' sizes (about 10 s cached)
```

## Principles

- **The background player plays:** ytmd owns the player, playback, the account and the engine on its loop; the Desktop and `ytm` only mirror and control it over `api` (`docs/daemon.md`).
- **Native and light:** MyGo's Native UI, GPU-drawn, no webview; each binary links only what it needs (`docs/daemon.md#programs`, held by `TestDeps`).
- **Idle means nothing:** no frames while idle, a paused ytmd wakes nothing, front-ends never poll.
- **Measured:** allocation budgets, benchmarks and footprint baselines back every performance decision (`docs/performance.md`).

## Architecture

The data flow, the Desktop's files and each package in full: `docs/architecture.md`; what each binary links: `docs/daemon.md#programs`; details in each package's doc comment (`go doc ./internal/<pkg>`).

- Programs: `cmd/ytmd` (the background player), `cmd/desktop` (the MyGo app, `package main`), `cmd/ytm` (the TUI and command line).
- Music:
  - `catalog`: music types by stable string ID, `Source` (and `Accounts`), the UI-thread-only `Catalog` cache, the placeholder; `catalogtest`: a `Source` tests answer by hand.
  - `innertube`: InnerTube transport, knowing nothing about music; `ytmusic`: `catalog.Source` over it, tested offline against `testdata/`.
  - `auth`: browser-cookie credentials and their store (Keychain on macOS).
  - `stream`: a song's Opus stream; `webm`: its frames out of WebM; `audio`: the engine, Now Playing, MPRIS, and `Fake`.
  - `artwork`: thumbnails, with memory and disk caches; `httpclient`: a process's HTTP client, through the system's proxy.
- The background player:
  - `loop`: ytmd's run queue; `daemon`: ytmd's service on it, serving `api` over `ipc`.
  - `player`: queue, shuffle, repeat, volume; `playback`: drives the engine from the player.
  - `account`: sign-in/out, who is signed in, likes; plain values, no UI state.
  - `api`: the protocol (frames, methods, events, error codes); `ipc`: paths, lock, socket, starting ytmd.
  - `autostart`: ytmd at login; `version`, `logfile`; `quiet`: the heap back to the OS once idle.
- Front-ends:
  - `remote`: a front-end's side of ytmd: `Client` (pages' music, controls, the playback mirror), `Account`, `Supervisor` (reconnects, hand-off, health).
  - `session`: the account as pages read it: who is signed in, the library and likes, `SetLiked`; `accountcache`: the library and likes on disk.
  - `loader`: page fetches for the page shown; `fetchstate`: `Resource`, `Fetch` and the `Registry` (`a.fetches`).
  - `job`: off-thread work whose `done` runs on the UI thread, dropped when stale.
  - `notice`: messages for the user as codes and arguments; `noticetext`: their words, which `tell` shows.
  - `theme`: tokens, presets, palettes, theme files; `settings`: preferences and user themes on disk; `duration`: lengths of time as front-ends say them.
  - `tui`: `ytm`'s player (Bubble Tea, Lip Gloss); `termimage`: images in a terminal, no I/O.
- Support: `atomicfile` (files replaced whole), `budget` (allocation budgets), `leakcheck` (goroutines left blocked fail the test binary).

## Conventions

- Read the relevant docs and skills before changing code. `.agents/skills/golang-*` come from [samber/cc-skills-golang](https://github.com/samber/cc-skills-golang) (MIT, `evals/` and `golang-documentation` dropped); `.agents/skills/brandkit` from [nexu-io/open-design](https://github.com/nexu-io/open-design/tree/main/skills/brandkit) (MIT). Where one disagrees with this file, this file wins.
- Before changing ytmd, `api`, `ipc` or `remote`, read `docs/daemon.md#protocol` and `docs/daemon.md#threading`; before moving code between packages, `docs/daemon.md#programs`.
- Follow Go and MyGo best practices. Build UI with MyGo's Native UI API, not its web frontend (no HTML/CSS/JS).
- Commits: [Conventional Commits](https://www.conventionalcommits.org) (`feat(player): add shuffle`), imperative, under 72 characters.
- Changes users notice go under `## [Unreleased]` in `CHANGELOG.md` in the same commit, only in `Added`, `Changed` or `Fixed`.
- Comments: one or two lines, the deciding why. Measurements and history go in the commit body.

### Docs

- Short. Update in the same change, in place, never appended.
- `docs/` describes only what is built, never "not yet" or "planned". What is not built or not yet checked goes in `roadmap/`, its headings matching the `docs/` page's (`roadmap/daemon.md` ↔ `docs/daemon.md`). `roadmap/` may link to `docs/`, never the reverse.
- When it is done, move the part from `roadmap/` to `docs/` in the same commit, written as it is.
- One fact in one place: other pages link to it rather than repeat it (what each binary links: `docs/daemon.md#programs`). Architecture above names each package's role; `docs/architecture.md` holds the rest.
- Code cites docs as path and anchor (`docs/daemon.md#protocol`; a whole page without one), only `docs/`.
- `docs/` and `roadmap/` names are kebab-case; every page is listed under Documentation below.
- One word for one thing: ytmd (the background player, in words users read), the Desktop, `ytm`, a front-end (the Desktop or `ytm`); sign in to the account, log in only to the OS.

### Threading

- ytmd's player, playback, account and engine live on its loop (`loop.Post`), which never waits on a socket; the Desktop and TUI only mirror them.
- Never call a `Source` or touch `a.cat` from a goroutine: fetch off-thread with `job.Job.Run`, whose `done` runs on the UI thread through `post`, dropped when stale, and merges.
- Goroutines return plain values; loader, account, playback, artwork and settings change shared state only in posted funcs.

### UI

- Read every color, font size, spacing and radius from `th`, never literals (circles' and skeletons' geometry aside).
- Pages read `a.fetches.Get`; failures show Retry, `Missing` shows `notFound`. Loading states are static (no shimmer).
- Every `notice.Notice` goes through `tell`.
- Every word the user reads comes from `locales` (`locales.Play()`), in both catalogs; names from YouTube and keys stay as they are. Text kept between builds is dropped when the language changes (`syncLanguage`); a message of string arguments said often is a `phrase`.
- Icons only from Lucide, under Lucide's names, through `scripts/fetch-lucide-icons.sh`; a filled one is `mustFilledIcon`, never drawn by hand.
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
- Analyze binary size with `gsa` first: text or filtered JSON, not full symbol dumps or custom parsers (`docs/benchmarks.md#attribution-with-gsa`).

### Logging

- At debug, only on failure paths, with typed fields (`Str`, `Err`, `Int`, `Stringer`), never strings built before the level check.
- Log IDs and errors; never credentials, cookies, headers, visitorData or stream URLs.
- A package that logs sets `zerolog.Disabled` in its `TestMain`.

### Tests

How they run, and the Desktop's test apps: `docs/testing.md`.

- No window, no network, no disk cache, no config dir.
- Desktop tests sit on ytmd in the test's process (`testApp(t)` and its kin) and show English; they wait for fetches and ytmd's round trips with `settleLoads`, `answer` or `src.Next`.
- Loader, account, session, playback, job and settings logic is tested in their packages.
- A package whose tests start goroutines ends its `TestMain` with `os.Exit(leakcheck.Run(m))`. `catalogtest.New(t)` fails the requests still unanswered when `t` ends.

### Benchmarks and budgets

Details: `docs/benchmarks.md`.

- Each package has `bench_test.go` (`Benchmark<Function>/<case>`, deterministic inputs, one scaled case) and `budget_test.go` (`TestBudget<Function>`).
- Budgets hold allocations at about 1.15× today's count (0–2 exactly), with a `// N allocs today.` comment. Raising one needs its reason in the same change.
- A new `pageCases`/`interactionCases` case needs its budget.

## Documentation

- `README.md`: what the player is, installing, building from source
- `docs/architecture.md`: the three programs, the data flow, every package and the Desktop's files
- `docs/daemon.md`: ytmd: what each binary links, threading, paths, starting and stopping, versions and hand-off, health, protocol, saved state, start at login, known limitations
- `docs/testing.md`: the Desktop's test apps on ytmd, waiting, golden files, goroutine leaks
- `docs/performance.md`: why the performance rules, measuring, memory, the background player's and the TUI's costs
- `docs/benchmarks.md`: benchmarks, allocation budgets, comparing time, binary size
- `docs/theme.md`: themes and theme files
- `docs/release.md`: releasing, older versions, the helpers, signing, updates, Homebrew
- `roadmap/daemon.md`: what ytmd still needs checked on real installs
- `CHANGELOG.md`: changes users notice, by version
- `LICENSE`: MIT
