# Performance

Why the rules in `AGENTS.md` are what they are, and how to measure.

## Measuring

- `go test -run '^$' -bench Pages .`: per-page time, view builds, bytes and allocs per frame, the heap a page retains, plus playing and 2000-song cases (`bench_test.go`). Add `-v` with `MYGO_FRAME_STATS=all` for build/layout/paint per frame; the same variable logs real frames in `go run .`.
- `go test -run '^$' -bench Interactions .`: hover, scroll, typing and tab switches. Its ns/op is mostly the Tester rasterizing damage in software, so compare allocs and builds. The Tester builds every frame, so builds/op don't show a pose's paint-only frames, and draws as many as fit in a motion's time (up to 20).
- `go run -tags memprof .` (`harness_memprof.go`; add `placeholder` for the offline fake) serves pprof on `127.0.0.1:6061`; `YT_HARNESS=1` also drives every page. When measuring two builds at once, give each its own `YT_PPROF_ADDR`, or one's harness drives the other's app.
- Memory: `footprint -p <pid>` (Activity Monitor's number). `scripts/footprint.sh` (`harness_footprint.go`) measures the next section's numbers in the real window, about 70 seconds: footprint and frames idle, after one frame, after every page, and while a song plays (on a silent engine) focused, paused, unfocused and minimized. Placeholder runs compare with `perf/footprint-baseline.txt` (`-update`), a `key=value` line a phase; `-live` adds real thumbnails. A phase the window was not right for (macOS keeps the focus with the app in front: click the window) is marked `invalid` and fails the run.
- Benchmarks and allocation budgets: `docs/benchmarks.md`.
- No PGO (`default.pgo`): a CPU profile of three placeholder `YT_HARNESS=1` tours spends about 90% in Metal and other C calls. Built with it, Pages, Interactions and Memo moved +0.06% geomean (10 interleaved runs, M4 Max, Go 1.27.2): 1–2% faster on 8 of 19 pages, 0.5–3% slower on 5 other benchmarks, allocations unchanged, +49 KB of binary.

## Memory on macOS

Numbers from `scripts/footprint.sh` (`perf/footprint-baseline.txt`: M4 Max, 1280×770 window, placeholder catalog), but for thumbnails.

- Any frame makes Metal hold ~270 MB for ~1 s ("Owned physical footprint (unmapped) (graphics)", ~250 MB of it): the same in a minimal MyGo app and at any window size; MyGo's own textures are ~5 MB. So idle means no frames.
- Playing draws one frame per shown step: ~330 MB focused (1 s), ~125–135 MB on average unfocused (5 s; the driver's memory goes about 1 s after a frame), ~70 MB minimized or hidden, none while paused; idle ~60 MB. Steps longer than the hold cost more CPU than 1 s: a frame after the release takes ~8–16 ms against ~1.5–3 ms.
- Thumbnails cost about +14 MB idle footprint per page of real results, mostly decoded pixels in the Go heap. `internal/artwork` keeps a 32 MB memory LRU (images shown in the last two frames stay) and a 128 MB disk cache; MyGo keeps the pixels of about one page's images (~20) after the page is left, until it shows again.
- The Go heap grows to twice what is live before a GC, and the scavenger returns free pages over minutes: `quiet.go` runs `debug.FreeOSMemory` (0.6–5 ms, off the UI thread) after 3 s without a build, if the heap allocated 1 MB since. With thumbnails: idle ~96 → 77 MB, after every page ~121 → 96 MB, playing minimized ~119 → 93 MB; frames unchanged.

## Audio

One engine on every system (`internal/audio`): YouTube's Opus in WebM, fetched in ranges, decoded in Go, played through MyGo's `audio` output. Numbers on an M4 Max.

- Opus over AAC: Opus decoders in pure Go exist under MIT/BSD, AAC's only under the GPL or LGPL, and Opus is at 48 kHz, MyGo's output rate, so nothing resamples. `pion/opus` decodes about 830× faster than real time (`BenchmarkOpusRead`): about 0.1% of a core while a song plays, a 3:33 song in 0.5 s. It matches ffmpeg's sample count exactly and its samples to about 72 dB; a seek decodes the same samples as playing through. `tphakala/go-opus` was 13% faster but fixed point (69 dB, `int16`), `darui3018823/opus` 2.5× slower, allocating per packet.
- Per packet nothing allocates: `webm` reuses one block buffer (about 8 GB/s, `BenchmarkReadFrame`), the decoder its output. A seek makes the decoder's SILK state anew (5 allocs).
- The source asks for ranges of up to 2 MiB, the next once the reader used half: a few requests a song, and none while paused. It keeps 128 KiB chunks, up to 8 MiB: a song of up to about eight minutes stays whole, so seeking back costs no request. A 4 MB song allocates about 4.3 MB, once (`BenchmarkSourceRead`); the heap in use while playing is 3–4 MB (`TestLiveStream`).
- No polling while a song plays: MyGo tells no one when a player ends or runs dry, so a timer fires at the computed end, and another, once the source has waited 200 ms for the network, when what was decoded ahead runs out (MyGo decodes about a second ahead). Only a stall is polled, every 100 ms, until half a second is decoded ahead: a seek's refill, or each piece of a slow response, would otherwise report a stall and its end.

## JSON

- encoding/json/v2 everywhere, with its defaults. Options only where a format needs them: theme files reject unknown names; `theme.Encode` and recorded fixtures sort keys; files people read are indented; InnerTube responses skip a string or number of another type (`lenient`), as maps did.
- InnerTube responses decode into structs naming only what the parsers read (`ytmusic/response.go`), the rest skipped: against `map[string]any`, a tenth of the bytes and an eighth of the allocations, about a third less time. `lenient` costs ~20%; a jsontext walk was no faster, and harder to read.
- Parsing a decoded page allocates little beside its results: `ytmusic`'s `meta` joins artist names as it reads them, so a song of one artist costs none (a playlist page: 101 allocations → 1, the slice).
- Request bodies we build are structs (`stream.playerRequest`, `innertube.requestContext`): maps took 31 of a player body's allocations and 8 of a call's context.
- No sonic: measured at about 2.5× v1's speed on arm64, but a 2 MB parser per decode once a GC empties its pools, more allocations on structs and small bodies, +1.25 MB of binary, and native only up to the Go its build tags name.

## Drawing

- `ui.Image(...).Radius()` is cut by the shader even when rotated; `Painter.Image` in a rounded `p.Clip`, or a rounded `Clip()` box, costs a mask texture under any transform.
- `Transform`/`Scale`/`AnimatePose` on, or inside a rounded `Clip` around, anything that clips (most painted covers do) makes MyGo draw the clip through a cached RGBA mask texture, even a square `p.Clip`.
- A value eased with `Animate` builds the view every frame of the motion; a pose's frames only paint (in a window, the play button's hover: 300 builds → 10).
- MyGo `fmt.Sprintf`s any key that is not a string or an int on every build (`pageHeaderKey` is an `int64`); a string passed as `any` allocates, hence boxed keys in `a.memo`.
- Each `Route.Match` splits its pattern, an allocation: `a.route` tries the pattern `a.path()` names first. A leaving page is built at its own location, so the others stay as a fallback, and `r.Query` stays.
- What a build shows per item is made once: a song row's key, label and "artist · album" (`a.rowText`, by song ID), cards' keys and lines (`cardText`), table positions (`numeral`). Funcs handed to MyGo (shortcuts, `OnChange`, `Draw`) are made once too (`a.actions`, `a.paints`, `seekPaint`, `eqPainters`, a theme card's `choose` and `preview`): a closure or method value per build allocates. A list's `ItemKey` and `Rows` stay per build: MyGo calls them after the build, when a leaving page's list may be built too. Together they took steady frames from 57–95 allocations to 11–32.
