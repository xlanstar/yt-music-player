# Performance

Why the rules in `AGENTS.md` are what they are, and how to measure.

## Measuring

- `go test -run '^$' -bench Pages ./cmd/desktop`: per-page time, view builds, bytes and allocs per frame, the heap a page retains, plus playing and 2000-song cases (`bench_test.go`). Add `-v` with `MYGO_FRAME_STATS=all` for build/layout/paint per frame; the same variable logs real frames in `go run ./cmd/desktop`.
- `go test -run '^$' -bench Interactions ./cmd/desktop`: hover, scroll, typing and tab switches. Its ns/op is mostly the Tester rasterizing damage in software, so compare allocs and builds. The Tester builds every frame, so builds/op don't show a pose's paint-only frames; its clock stands still between ops, so a motion settles its 20 frames at the input's instant.
- `go run -tags memprof ./cmd/desktop` (`harness_memprof.go`; add `placeholder` for the offline fake), with a ytmd of the same tags running (`go run [-tags placeholder] ./cmd/ytmd`), serves pprof on `127.0.0.1:6061`, ytmd left out; `YT_HARNESS=1` also drives every page. Two builds at once need their own `YT_PPROF_ADDR`, or one's harness drives the other's app.
- `go build -tags memprof -o build/dev/ ./cmd/ytmd ./cmd/ytm` (`cmd/ytmd/memprof.go`) makes a ytmd that serves pprof on `127.0.0.1:6062` (`YTMD_PPROF_ADDR`), and at `/debug/metrics` every `runtime/metrics` value, which split the runtime's own memory (stacks, GC metadata, fragmentation) where MemStats lumps it. `GODEBUG=memprofilerate=1` on it makes heap profiles exact rather than sampled every 512 KB. Release builds link no profiler (`TestDeps`).
- Memory: `footprint -p <pid>` (Activity Monitor's number). `scripts/footprint.sh` (`harness_footprint.go`, about 70 s) measures the next section's numbers in the real window, over a development ytmd it builds; `-live` uses the account. A phase the window was not right for (macOS keeps the focus with the app in front: click the window) is `invalid` and fails the run.
- `scripts/footprint.sh -daemon` measures ytmd alone, a placeholder one in a home of its own (`-live`: a real one, signed out): footprint, RSS and top's idle wake-ups paused, playing and after a Desktop toured every page through it. A paused phase that wakes fails the run. Placeholder runs compare with `perf/footprint-baseline.txt` and `perf/footprint-daemon-baseline.txt` (`-update` replaces one).
- Benchmarks and allocation budgets: `docs/benchmarks.md`.
- No PGO (`default.pgo`): a CPU profile of three placeholder `YT_HARNESS=1` tours spends about 90% in Metal and other C calls. Built with it, Pages, Interactions and Memo moved +0.06% geomean (10 interleaved runs, M4 Max, Go 1.27.2): 1–2% faster on 8 of 19 pages, 0.5–3% slower on 5 other benchmarks, allocations unchanged, +49 KB of binary.

## Struct layout

- Group small fields and move trailing zero-size fields forward; verify savings with `unsafe.Sizeof`.
- Audit with `fieldalignment -json ./...`, including build tags and target platforms. Prefer size savings over pointer-prefix-only reordering.
- Preserve CLI JSON order and frozen old-peer types; gob reordering must still pass the bootstrap test.

## Memory on macOS

Numbers from `scripts/footprint.sh` (`perf/footprint-baseline.txt`: M4 Max, 1280×770 window, placeholder catalog), but for thumbnails.

- Any frame makes Metal hold ~270 MB for ~1 s ("Owned physical footprint (unmapped) (graphics)", ~250 MB of it): the same in a minimal MyGo app and at any window size; MyGo's own textures are ~5 MB. So idle means no frames.
- Playing draws one frame per shown step: ~330 MB focused (1 s steps), ~127 MB on average unfocused (5 s; the driver's memory goes ~1 s after a frame), ~70 MB minimized or hidden; paused and idle draw none, ~60 MB. Steps longer than the hold cost more CPU: a frame after the release takes ~8.5 ms against ~1.5.
- Thumbnails cost about +14 MB idle footprint per page of real results, mostly decoded pixels in the Go heap. `internal/artwork` keeps a 32 MB memory LRU (images shown in the last two frames stay) and a 128 MB disk cache; MyGo keeps the pixels of about one page's images (~20) after the page is left, until it shows again.
- `artwork` fetches Google's covers as WebP at the host's default quality, in the Desktop and ytm: sharper than their "l90" JPEG and 30–45% smaller up to 544 px, for 1.3–2.5× the decode time, off the UI thread. `Decode` expands x/image/webp's limited-range YCbCr, else ~8 levels washed out. Video stills stay JPEG: `vi_webp` saves ~17%, and signed crops can't change.
- `artwork` scales a cover from the decoder's pixels, a row at a time, with a box filter that counts every pixel once: a 1200 px JPEG shown at 352 allocates 2.7 MB, against 9.9 for a copy of it whole, halved, then bilinear (x/image/draw, 150 KB of binary).
- The Go heap grows to twice what is live before a GC, and the scavenger returns free pages over minutes: `internal/quiet` runs `debug.FreeOSMemory` (0.6–5 ms, off the UI thread) after 3 s without a build, if the heap allocated 1 MB since. With thumbnails, idle ~96 → 77 MB, after every page ~121 → 96 MB.

## The background player

Numbers from `scripts/footprint.sh -daemon` (`perf/footprint-daemon-baseline.txt`: M4 Max, a placeholder ytmd, whose engine is a fake on a clock).

- ytmd's footprint: ~6 MB fresh and paused (RSS ~13 MB), ~6.4 MB playing, ~8.5 MB after a Desktop toured every page through it (RSS ~16 MB). A real engine adds about 2 MB of heap while it holds a stream (below) and MyGo's audio output, which closes 5 s into a pause. The window's numbers above hold no player.
- A real ytmd (`-daemon -live`): ~8 MB fresh (RSS ~17 MB), ~19 MB playing and paused after (RSS ~51 MB, ~34 MB of it the frameworks' shared clean pages: CoreAudio, MediaPlayer). The Go runtime holds about two thirds of the footprint ("Untagged"), CoreAudio's malloc and the frameworks' dirty data the rest, 3–4 MB each.
- ytmd keeps its queue's songs and, signed in, the library and likes, fetched once a front-end connects; `catalog.ShareStrings` keeps one copy of each artist, album and cover the decoded songs repeat: 5000 liked take 1.38 MB instead of 1.85 (`TestBudgetLiveLikes`), a 5000-song queue 2.1 instead of 2.6 (`TestBudgetLiveQueue`). `ytm status`, which status bars poll, never copies the likes.
- gob keeps a connection's buffer as large as the largest frame it sent, as long as the front-end stays, so songs go ahead in frames of 256 at most ([docs/daemon.md](daemon.md#protocol)): with 5000 liked and 5000 queued, a subscriber's connection keeps 193 KB instead of 2.75 MB (`TestBudgetLiveConn`). The library and replies still go whole.
- Paused, ytmd wakes nothing (0 idle wake-ups over 10 s, a TUI connected too): state goes out on changes; the save 2 s after a change, the stream's release a minute into a pause (`internal/playback`) and the heap's release 3 s after the last batch are one-shot timers. Only Go's sysmon wakes it, about once a minute.
- ytmd's GOGC is 50 (`cmd/ytmd/main.go`): MyGo's audio output allocates 35 KB/s while a song plays, which `internal/quiet`, the loop asleep, never frees, so the heap stays at its goal, the live heap plus GOGC percent. 50 keeps ~2 MB less of ~25, for no measurable CPU; a memory limit would not help, the live heap growing with the song.
- System frameworks stay: CoreFoundation's run loop serves Now Playing from start, and Security (the Keychain, read at start) links it anyway; MediaPlayer and AudioToolbox load as the first song plays, and Objective-C images never unload. Most of their pages are shared and clean (RSS, not footprint): CoreFoundation and MediaPlayer cost about 1 MB of footprint each.
- Front-ends never poll ytmd ([docs/daemon.md](daemon.md#health)); the TUI itself is not idle (Bubble Tea's renderer ticks at 30 fps while it runs).

## The TUI's covers

- Detected off, nothing is made: no `artwork` loader, no HTTP client. The covers link `net/http`, TLS and the decoders into ytm (6.4 → ≈9.7 MB on darwin/arm64, built `-trimpath -ldflags='-s -w'`); fetching them in ytmd instead would save ytm 2 MB of binary but keep connections and a cache in the process that never quits.
- WebP's decoder costs ytm 100–130 KB (x/image/webp, which the Desktop links through MyGo's `ui` anyway); its covers, at 80–96 px, are ~40% smaller than JPEG.
- Covers are fetched at their cells' pixels (artwork's bucket: 80 for 4×2 Retina cells), 2 workers, keeping 64 KB of decoded pixels besides those shown (`artMemory`). They encode off the UI thread, once per cover and size; the scene composes their lines in their cells: a frame with a cover allocates as one without (`TestBudgetView`).
- Each protocol keeps what it needs again: Kitty's upload goes once, the terminal keeping the pixels under 8 recycled ids (from a random base, so two ytm in a terminal keep theirs); an overlay keeps its Sixel or JPEG for after a repaint, half blocks their lines. Covers off screen keep 64 KB of these, unsent uploads too (`offScreenBytes`).
- Sixel and iTerm2 images go again only after a frame that wrote over them: each of their lines carries a mark (a PM) in its first cell, which the renderer writes only with that cell, and `termOut` adds the images whose marks a frame carried before its end (`termimage.Splice`, 0 allocs). No timer: an idle screen writes nothing.
- A line with a wide character (a Japanese title) is repainted whole in every frame (ultraviolet's `lineHasDrift`), its image too: once a second while playing, up to 30 times a second while scrolling. At 80 pixels an image is 6.6 KB of Sixel or 2.1 of iTerm2, at 160 20.9 or 4.6.
- Without synchronized output (mode 2026, which Bubble Tea asks for neither over SSH nor in Apple's Terminal), such an image may flicker as its line repaints.
- The encoders are `termimage`'s, not x/ansi's, which box every pixel through `At` and, for Sixel, quantize with maps and pull in a bitset module. For 80 pixels: Sixel 3 allocs and ~40 µs (one pass per band), a Kitty upload 1, without zlib, whose state outweighs a cover, iTerm2's JPEG 8, image/jpeg's. `fill` keeps a cover already the size.
- A render's scene keeps its spans from frame to frame: drawing allocates nothing, composing the screen's one string (`TestBudgetScene`).
- `internal/quiet` frees the heap 3 s after Update last ran, as it does after the Desktop's last build.

## Audio

One engine on every system (`internal/audio`): YouTube's Opus in WebM, fetched in ranges, decoded in Go, played through MyGo's `audio` output. Numbers on an M4 Max.

- Opus over AAC: pure-Go Opus decoders exist under MIT/BSD, AAC's only under the GPL or LGPL, and Opus is at 48 kHz, MyGo's output rate, so nothing resamples. `pion/opus` decodes about 830× faster than real time (`BenchmarkOpusRead`), 0.1% of a core while a song plays; it matches ffmpeg's sample count exactly and its samples to about 72 dB.
- Per packet nothing allocates: `webm` reuses one block buffer (about 8 GB/s, `BenchmarkReadFrame`), the decoder its output. A seek makes the decoder's SILK state anew (5 allocs).
- The source keeps a window of 128 KiB chunks, 1 MiB ahead of the reader (a minute at 128 kbit/s) and 256 KiB behind, for short seeks back, refilling once half the read-ahead is left. Chunks leaving the window are reused: a song of any length allocates about 1.5 MB once (`BenchmarkSourceRead`), nothing as it plays.
- A seek out of the window costs one request, backwards as forwards. 1 MiB ahead is enough: the 15–30 s left when a request starts outlast a stalled response's 15 s timeout (`requestTimeout`); 2 MiB would halve the requests for 1 MB more heap.
- A stream held is about 2 MB (the window, MyGo's second of decoded samples, the decoder). A minute paused (an ended song pauses too), `internal/playback` releases it; Play loads it again where it was, its URL resolved anew if near expiry. The timer runs from the pause, not the loop's last batch, so browsing paused still releases it.
- `quiet` needs no hook while a song plays: nothing posts to the loop then, but chunks are reused and nothing per packet allocates, so streaming allocates about 100 KB per MB (requests, TLS, HTTP/2 frames), below the 1 MB a free waits for. What a song held goes at its end, with the loop's batch for the next.
- MyGo reports no end or underrun, yet a song on time polls nothing: timers fire at the computed end and, the source 200 ms into waiting for the network, when the decoded lead runs out. Only a stall (every 100 ms, until half a second is decoded) and an unknown or late end (every 250 ms) are polled.

## Network

One `httpclient` client per process; YouTube's hosts speak HTTP/2, one connection each. Measured on an M4 Max, ~15 ms from Google:

- A kept connection answers in ~16 ms, a new one in 50–75 ms.
- `MaxConnsPerHost` 1: the artwork's 4 workers on a cold host made 4 TLS handshakes, now 1, in no more time.
- YouTube keeps idle connections 1.5–5 minutes; Go's 90 s is now 2 minutes.
- Resuming a TLS session saves a reconnect 3–6 ms.
- Prefetching the next song probes its URL, so its stream starts on an open connection.

## Messages

- A message without arguments is a map lookup once said (MyGo keeps it by language): no allocation, so pages say theirs every build. Reading the language is one atomic load once the app knows it; tests set it the same way (`TestMain`), as MyGo's own path before `App.Run` allocates on each call.
- A message with arguments goes through ICU formatting, about ten allocations a call. Those said often with string arguments (rows' names, the seek bar's value, search's lines) are `phrase`s: formatted once per language with markers, then filled in with one allocation. Plural ones (song counts, lengths) stay ICU's, on memo misses only.
- Catalogs and CLDR's data for formatting add about 250 KB to the release binary.

## JSON

- encoding/json/v2 everywhere, with its defaults. Options only where a format needs them: theme files reject unknown names; `theme.Encode` and recorded fixtures sort keys; files people read are indented; InnerTube responses skip a string or number of another type (`lenient`), as maps did.
- InnerTube responses decode into structs naming only what the parsers read (`ytmusic/response.go`), the rest skipped: against `map[string]any`, a tenth of the bytes and an eighth of the allocations, about a third less time. `lenient` costs ~20%; a jsontext walk was no faster, and harder to read.
- Parsing a decoded page allocates little beside its results: `ytmusic`'s `meta` joins artist names as it reads them, so a song of one artist costs none (a playlist page: 101 allocations → 1, the slice).
- Responses decode as they are read (`innertube.Client.Call`'s decode, `stream.parsePlayer`): read whole first, they allocated another 2–2.5× their size.
- ytmd encodes `daemon.json` into its file: through bytes, a 2000-song queue's (172 KB) allocated 173 KB a save, now 1.2 KB.
- Request bodies we build are structs (`stream.playerRequest`, `innertube.requestContext`): maps took 31 of a player body's allocations and 8 of a call's context.
- No sonic: measured at about 2.5× v1's speed on arm64, but a 2 MB parser per decode once a GC empties its pools, more allocations on structs and small bodies, +1.25 MB of binary, and native only up to the Go its build tags name.

## Drawing

- `ui.Image(...).Radius()` is cut by the shader even when rotated; `Painter.Image` in a rounded `p.Clip`, or a rounded `Clip()` box, costs a mask texture under any transform.
- `Transform`/`Scale`/`AnimatePose` on, or inside a rounded `Clip` around, anything that clips (most painted covers do) makes MyGo draw the clip through a cached RGBA mask texture, even a square `p.Clip`.
- A value eased with `Animate` builds the view every frame of the motion; a pose's frames only paint (in a window, the play button's hover: 300 builds → 10).
- MyGo `fmt.Sprintf`s any key that is not a string or an int on every build (`pageHeaderKey` is an `int64`); a string passed as `any` allocates, hence boxed keys in `a.memo`.
- Each `Route.Match` splits its pattern, an allocation: `a.route` tries the pattern `a.path()` names first. A leaving page is built at its own location, so the others stay as a fallback, and `r.Query` stays.
- What a build shows per item is made once: a song row's key, label and "artist · album" (`a.rowText`, by song ID), cards' keys and lines (`cardText`), table positions (`numeral`).
- Funcs handed to MyGo (shortcuts, `OnChange`, `Draw`) are made once too (`a.actions`, `a.paints`, `seekPaint`, `eqPainters`, a theme card's `choose` and `preview`): a closure or method value per build allocates. A list's `ItemKey` and `Rows` stay per build: MyGo calls them after the build, when a leaving page's list may be built too.
