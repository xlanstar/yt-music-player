# Testing

The rules are in `AGENTS.md` (Tests); this is how the Desktop's tests run. `prek` runs every test under the race detector, then the allocation budgets without it ([benchmarks.md](benchmarks.md#budgets)).

## Apps on ytmd

Apps sit on ytmd in the test's process (`ytmd_test.go`): a `daemon.Service` over `net.Pipe`, the placeholder's queue, no saved state.

- `testApp(t)`, `sourceApp(t, src)`: the placeholder, or a source of the test's.
- `idleApp(t)`: feeds preloaded.
- `anonApp(t)`: signed out, over `catalogtest.New`; `scriptedApp(t)`: an `anonApp` whose feeds are fetched, so only opened pages ask.
- `accountApp(t)`: over `catalogtest.Accounts`, so ytmd asks first for the account, library and likes.
- `playbackApp(t, res)`: ytmd plays on `audio.Fake`.
- `lookApp(t, prepare)`: settings in a `TempDir`.

Tests show English; `inLanguage(t, "zh-Hant")` switches.

## Waiting

- `newScreen`, `showApp` and `newTester` show an app on a Tester whose builds first run the Client's posts.
- `settleLoads` and `busy` wait for fetches, thumbnails, the session and ytmd's round trips (`syncDaemon`, `settled`).
- `answer` waits until ytmd took the reply and the app has it.
- Scripted sources wait drawing frames: `src.Next(t, tt.Frame, …)`, `Take`, `None`. `catalogtest.New(t)` fails the requests still unanswered when `t` ends.

## Golden files

The TUI's screens are golden files in `internal/tui/testdata/`, and `cmd/ytm/testdata/status.json` holds `ytm status --json`; `-update` rewrites them (`go test ./internal/tui/ -update`).

## Goroutine leaks

A package whose tests start goroutines ends its `TestMain` with `os.Exit(leakcheck.Run(m))`: one left blocked for good (Go's `goroutineleak` profile) fails it.
