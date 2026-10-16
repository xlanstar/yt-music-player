# Benchmarks and budgets

## Benchmarks

- Each package has `bench_test.go` and `budget_test.go`. Benchmarks are `Benchmark<Function>/<case>`, `for b.Loop()` with `b.ReportAllocs()`, results in package-level sinks, `b.SetBytes` for byte parsers and decoders.
- Inputs come from `testdata/` or are generated deterministically in the test, plus one scaled case (2000 songs; 5000 in `catalog`). Never the network, the Keychain or the user's cache: loopback `httptest`, in-memory transports, `TempDir`.
- End-to-end benchmarks sit next to the pure parse ones; in `ytmusic`, decoding a response (`BenchmarkDecode`) costs several times parsing it.
- The `go-bench-once` hook runs every benchmark once (`go test -run '^$' -bench . -benchtime 1x ./...`, about 5 s), so that none rots.

## Budgets

- `TestBudget<Function>` holds allocations, which are deterministic where time is not: `budget.Allocs(t, limit, runs, f)` (skipped under `-race`). Where bytes set the footprint (`artwork`'s decoding and loading, `ytmusic`'s `decode`), `budget.Within(t, budget.Cost{Allocs: …, Bytes: …}, runs, f)` holds both. `budget.Measure` gives the numbers; it runs on one P after a GC, so the runtime's own allocations stay out.
- Averages keep their fraction: a slice growing once in 100 calls is 0.01, and a 0 budget fails on it. Hot paths assert an interface once, not per call, converting one to another too (an `io.ReadSeeker` passed as an `io.Reader`): the runtime builds its assertion cache now and then (1 call in 1024).
- A ceiling is about 1.15× today's count, rounded up; 0–2 are held exactly; a comment gives today's (`// 27 allocs today.`, `// 2.17 MB today.`). One that varies between runs (a map's tables split as its random hash seed fills them) is 1.15× the most seen, its range in the comment. Lower one with an improvement; raising one needs its reason stated in the same change. No budgets on calls over a connection (loopback `httptest` included): a client over an in-memory `RoundTripper` is deterministic, a socket is not.
- Where a benchmark has a case table, the limits live in it, and the budget ranges over the same cases: a case without its limit does not compile.
- Screens (`cmd/desktop/budget_test.go`), together about 7 s:
  - `TestBudgetFrames`: every `BenchmarkPages` case to allocs and bytes per steady frame, 1 build a frame, and the heap the screen retains (`retainedKB`: with it, less once it is collected; the same alone or after other pages).
  - `TestBudgetInteractions`: every `BenchmarkInteractions` case to allocs, bytes and builds per op. The Tester's clock stands still between ops (MyGo 0.5.5), so a hover settles its 20 frames, each built, at the input's instant: the counts repeat on any machine; the hero's slow ones are held per build.
  - Both use the cases as data (`pageCases`, `interactionCases`, `newScreen`, `interacting`): a new case needs its budget.
  - Screens run over ytmd in the test's process (`ytmd_test.go`), connected by `net.Pipe`: deterministic, unlike a socket. `remote-event` counts an event's whole way, another front-end's request and the daemon's work included; `live-KB` leaves the daemon out, another process in the app.
  - `TestBudgetIdleFrames`: settled pages with thumbnails, and a paused song, draw no frame while `tt.Advance` moves the Tester's clock past their quiet time (no build, no `FramePending`, the same pixels for repaint-only frames), with playing in the focused window as the control.
  - `TestBudgetPlayingFrames`: a frame per shown step (60 a minute focused, 12 unfocused, none hidden or paused).

## Comparing time

ns/op is too noisy to gate. `scripts/bench.sh` runs Pages, Interactions, Memo and every `./internal/...` benchmark (6 runs of 100 ms each, about 4 minutes) into `perf/benchmarks-latest.txt`, headed by Go version, CPU, git state and load, and compares it with `perf/benchmarks-baseline.txt` through a pinned benchstat.

- Both files hold, per package, one line per benchmark and unit (`BenchmarkPages/Home-16 allocs/op 26`): each run's value in run order, or one value when every run gave it, without go test's column widths and iteration counts. A refreshed baseline's diff then shows the time lines and the allocations that moved, nothing else; the script expands the files back for benchstat.
- Six runs are the fewest benchstat gives a confidence interval for; more short runs beat fewer long ones, and 300 ms runs took 10 minutes for no narrower intervals.
- A package (`scripts/bench.sh ./internal/artwork/`) or `-run Pages/Home` focuses it; `-count 10` for a closer look.
- The `RunParallel` benchmarks (`innertube` `AuthSignedIn`, `stream` `Resolve/cached_parallel`) run on their own at `-cpu 1,4,<all>` in the full suite, to show how contention scales; with a package or `-run`, everything runs in one pass at all CPUs.
- It warns when the baseline's Go, CPU, `-count` or `-benchtime` differ from the run's, and lists benchmarks without baseline rows and, after a full run, baseline rows of benchmarks gone: the baseline is stale.
- `-against <rev>` compares with a git revision instead, built in a temporary worktree and run now, in turn with the working tree (`-count 1` each, old and new alternating, so drift hits both), into `perf/benchmarks-against.txt`. Prefer it to the baseline, which measures another day and toolchain too (Go 1.27's allocator alone moved allocation-heavy numbers). Takes packages and `-run`; twice as long.
- `-profile <package>` also writes `perf/cpu.prof` and `perf/mem.prof` (one package: go test profiles one at a time); `go tool pprof -top perf/cpu.prof`, `go tool pprof -alloc_objects -top perf/mem.prof`.
- `-update` makes a full run the baseline, committed with the change it records.

Compare allocs/op and B/op first, then sec/op, only on a quiet machine (it warns when busy).

## Commits

A change with a measurable performance effect is a `perf(scope):` commit whose body pastes benchstat's output (`scripts/bench.sh -against HEAD` before committing): only the affected rows, never one marked `~` (not significant), with the goos, goarch and cpu lines.
