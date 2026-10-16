# YT Music Player

An efficient, lightweight YouTube Music desktop player with a modern UI, written in Go 1.27 with MyGo native UI (GPU-drawn, no webview).

MyGo docs: https://mygo.egoist.dev/docs.md (always read the `.md` pages, never `.html`).

## Commands

- `prek`: the only check needed; it must pass.
- `go run ./cmd/desktop`: YouTube Music (signed-in account from the Keychain); songs play through the Go engine (`internal/audio`) on every system.
- `go run -tags placeholder ./cmd/desktop`: offline fake, always signed in, no audio; with it, `YT_SOURCE_DELAY=800ms` shows loading states. Without the tag (the release build) the fake is not linked in.
- `YT_LOG=debug|info|warn|error` (default `warn`): `debug` tells why fetches, likes, sign-ins and streams failed.
- Live checks (never in CI): `go test -run Record -update -v ./internal/ytmusic/` (re-records `testdata/`, `Record/catalog` or `Record/feeds` for one half; `YTM_COOKIE=…` with `Record/library` for signed-in, writes nothing); `go test ./internal/stream/ -run Live -live -v`; `go test -tags audiolive -run Live -v ./internal/audio/` (plays sound); `TestMPRIS` runs where there is a session bus (`dbus-run-session -- go test -run MPRIS ./internal/audio/`); `YTM_KEYCHAIN_TEST=1` for the Keychain.
- `locales/`: the messages, `en.json` (the source, with descriptions) and `zh-Hant.json`, in ICU MessageFormat; `go tool mygo generate .` writes `locales/catalog_gen.go` (committed) from them, which `prek` checks.
- `scripts/render-app-icon.sh`: renders the icon's one source, `assets/icon/icon.svg`, into `resources/icon.png` and `assets/icon/` (PNGs of every size, `.icns`, `.ico`); edit the SVG, never the renders.
- `scripts/repo-images.sh [-readme]`: renders the social preview (uploaded in the repository's settings, not committed) and README's top image from `assets/repo-images/` and a capture of the placeholder build; `-readme` updates `docs/images/`. Run it with `-readme` when the window's look changes.
- Release: `scripts/release.sh patch|minor|major`, then `publish X.Y.Z` after checking the draft, which also updates the Homebrew tap (`docs/release.md`); the version lives only in `mygo.json`, user-visible changes go under `## [Unreleased]` in `CHANGELOG.md` in the same change. The cask is `packaging/homebrew/yt-music-player.rb`; never edit it in the tap.
- Performance: `docs/performance.md`; benchmarks and budgets: `docs/benchmarks.md`; memory and frames of the real window: `scripts/footprint.sh` (macOS, about 70 seconds). Both compare with their committed `perf/*-baseline.txt`; a run writes `perf/*-latest.txt` (gitignored).

## Layout

Details live in each package's doc comment (`go doc ./internal/<pkg>`).

`internal/`:

- `catalog`: music types by stable string ID; `Source` (plus `Accounts`, for signing in), the UI-side `Catalog` cache (UI thread only, `Gen()`), the placeholder; `catalogtest`: a `Source` tests answer by hand.
- `job`: one piece of off-thread work whose `done` runs on the UI thread, dropped when stale.
- `fetchstate`: `Resource` and `Fetch` state; the `Registry` pages read (`a.fetches`).
- `notice`: messages for the user, made by session and playback, as codes and arguments; `noticetext`: their words, which `tell` shows (`Render`).
- `loader`: page fetches (album, playlist, feeds, search) for the page shown.
- `account`: sign-in/out, fetching the account, library and likes, sending likes; reports changes as plain values and holds no UI state, so a process without a window can own it.
- `session`: what `account` reports, as pages read it (fetch states, the catalog, notices); every like through `SetLiked`, shown at once and undone on failure.
- `auth`: browser-cookie credentials and their store (Keychain on macOS).
- `innertube`: InnerTube transport; knows nothing about music.
- `ytmusic`: `catalog.Source` over InnerTube, after ytmusicapi. Song = videoId, Album = `MPREb_…`, Playlist = playlistId without `VL`. Responses decode into the structs of `response.go`. Tests are offline against `testdata/`.
- `stream`: resolves a song's Opus stream; `audio`: the engine (fetches, decodes Opus in Go, plays through MyGo's `audio`; Now Playing on macOS, MPRIS on Linux) and `Fake`; `webm`: an audio track's frames out of WebM.
- `player`: queue, shuffle, repeat, volume; `playback`: drives the engine from the player, reports when the next frame is due.
- `theme`: design tokens, presets, derived palettes, theme files (`docs/theme.md`); `settings`: preferences and user themes on disk.
- `artwork`: thumbnail loader with memory and disk caches.
- `budget`: allocation budgets for tests.

`cmd/desktop` (`package main`, the MyGo app; `mygo.json`, `resources/` and `locales/` stay at the root, `mygo.json`'s `main` names this package):

- `main.go` (the window, the menu bar, the updater plugin), `source.go` (`newSource`; `source_placeholder.go` under the `placeholder` tag), `log.go`, `post.go` (`a.post`, buffered until the window attaches), `quiet.go` (heap back to the OS once builds stop), `network.go` (back online, what failed is fetched again).
- `app.go`: state, `reconcile` before each build, root layout from `th.Layout`. `memo.go`: `a.memo`, what builds derive, made again when its inputs change.
- `fetch.go` (router → resource, `syncLoad`), `account.go` + `signin.go` (account and session wiring, `tell`: the one path for every notice), `playback.go`, `appearance.go`.
- `routes.go`: `/home`, `/explore`, `/library?tab=`, `/album/{id}`, `/playlist/{id}`, `/liked`, `/search?q=`.
- Pages: `home.go`, `explore.go`, `library.go`, `collection.go`, `search.go`. Chrome: `sidebar.go`, `topbar.go`, `playerbar.go`, `nowplaying.go`.
- `rows.go` (the one song row), `widgets.go`, `theme.go` (`th`), `art.go`, `icons.go` + `icons/` (Lucide SVGs, embedded; `mygo.json` ships them too), `phrase.go` (messages of string arguments in one allocation).

## Rules

- Read the relevant docs and skills before changing code.
- `.agents/skills/golang-*` come from [samber/cc-skills-golang](https://github.com/samber/cc-skills-golang) (MIT, `evals/` dropped). Where one disagrees with this file (docs stay short, threading through `job.Run`/`post`), this file wins.
- Keep docs short and update them in the same change as the code. Comments explain why, never what.
- Follow Go and MyGo best practices. Build UI with MyGo's Native UI API, not its web frontend (no HTML/CSS/JS).
- Commits: [Conventional Commits](https://www.conventionalcommits.org) (`feat(player): add shuffle`), imperative, under 72 characters.

### Threading

- Never call a `Source` or touch `a.cat` (or the player) from a goroutine: fetch off-thread with `job.Run`, whose `done` runs on the UI thread through `post`, dropped when stale, and merges.
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

### Logging

- At debug, only on failure paths, with typed fields (`Str`, `Err`, `Int`, `Stringer`), never strings built before the level check.
- Log IDs and errors; never credentials, cookies, headers, visitorData or stream URLs.
- A package that logs sets `zerolog.Disabled` in its `TestMain`.

### Tests

- No window, no network, no disk cache, no config dir.
- Apps: `testApp()`/`sourceApp(src)`, `idleApp()` (feeds preloaded), `scriptedApp()` (only opened pages ask), `anonApp()`, `accountApp()` (over `catalogtest.Accounts`), `playbackApp(res)` (on `audio.Fake`), `lookApp(t, prepare)` (settings in a `TempDir`). Tests show English; `inLanguage(t, "zh-Hant")` switches.
- `newScreen`/`showApp` show one on a Tester that counts builds; `settleLoads`/`busy` wait for fetches, thumbnails and the session; scripted sources wait drawing frames: `src.Next(t, tt.Frame, …)`, `Take`, `None`.
- Loader, account, session, playback, job and settings logic is tested in their packages.

### Benchmarks and budgets

Details: `docs/benchmarks.md`.

- Each package has `bench_test.go` (`Benchmark<Function>/<case>`, deterministic inputs, one scaled case) and `budget_test.go` (`TestBudget<Function>`).
- Budgets hold allocations at about 1.15× today's count (0–2 exactly), with a `// N allocs today.` comment. Raising one needs its reason in the same change.
- A new `pageCases`/`interactionCases` case needs its budget.
