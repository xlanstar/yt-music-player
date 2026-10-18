# Performance

Why the rules in `AGENTS.md` are what they are, and how to measure.

## Measuring

- `go test -run '^$' -bench Pages ./cmd/desktop`: per-page time, view builds, bytes and allocs per frame, the heap a page retains, plus playing and 2000-song cases (`bench_test.go`). Add `-v` with `MYGO_FRAME_STATS=all` for build/layout/paint per frame; the same variable logs real frames in `go run ./cmd/desktop`.
- `go test -run '^$' -bench Interactions ./cmd/desktop`: hover, scroll, typing and tab switches. Its ns/op is mostly the Tester rasterizing damage in software, so compare allocs and builds. The Tester builds every frame, so builds/op don't show a pose's paint-only frames; its clock stands still between ops, so a motion settles its 20 frames at the input's instant.
- `go run -tags memprof ./cmd/desktop` (`harness_memprof.go`; add `placeholder` for the offline fake), with a ytmd of the same tags running (`go run [-tags placeholder] ./cmd/ytmd`), serves pprof on `127.0.0.1:6061`, ytmd left out; `YT_HARNESS=1` also drives every page. Two builds at once need their own `YT_PPROF_ADDR`, or one's harness drives the other's app.
- `go build -tags memprof -o build/dev/ ./cmd/ytmd ./cmd/ytm` (`cmd/ytmd/memprof.go`) makes a ytmd that serves pprof on `127.0.0.1:6062` (`YTMD_PPROF_ADDR`), and at `/debug/metrics` every `runtime/metrics` value, which split the runtime's own memory (stacks, GC metadata, fragmentation) where MemStats lumps it. `GODEBUG=memprofilerate=1` on it makes heap profiles exact rather than sampled every 512 KB. Release builds link no profiler (`TestDeps`).
- Memory: `footprint -p <pid>` (Activity Monitor's number). `scripts/footprint.sh` (`harness_footprint.go`) measures the next section's numbers in the real window, about 70 seconds: idle, after one frame, after every page, and while a song plays focused, paused, unfocused and minimized, over a development ytmd it builds (silent on the placeholder). `-live` uses the account: real thumbnails, songs aloud. A phase the window was not right for (macOS keeps the focus with the app in front: click the window) is `invalid` and fails the run. `-daemon` measures a placeholder ytmd alone, in a home of its own: footprint, RSS and top's idle wake-ups while paused, playing, and after a Desktop toured every page through it (about 90 seconds; without a window server, 50 and no Desktop); a paused phase that wakes fails the run. Placeholder runs compare with `perf/footprint-baseline.txt` and `perf/footprint-daemon-baseline.txt` (`-update` replaces one).
- Benchmarks and allocation budgets: `docs/benchmarks.md`.
- No PGO (`default.pgo`): a CPU profile of three placeholder `YT_HARNESS=1` tours spends about 90% in Metal and other C calls. Built with it, Pages, Interactions and Memo moved +0.06% geomean (10 interleaved runs, M4 Max, Go 1.27.2): 1–2% faster on 8 of 19 pages, 0.5–3% slower on 5 other benchmarks, allocations unchanged, +49 KB of binary.

## Memory on macOS

Numbers from `scripts/footprint.sh` (`perf/footprint-baseline.txt`: M4 Max, 1280×770 window, placeholder catalog), but for thumbnails.

- Any frame makes Metal hold ~270 MB for ~1 s ("Owned physical footprint (unmapped) (graphics)", ~250 MB of it): the same in a minimal MyGo app and at any window size; MyGo's own textures are ~5 MB. So idle means no frames.
- Playing draws one frame per shown step: ~330 MB focused (1 s), ~125–135 MB on average unfocused (5 s; the driver's memory goes about 1 s after a frame), ~70 MB minimized or hidden, none while paused; idle ~60 MB. Steps longer than the hold cost more CPU than 1 s: a frame after the release takes ~8–16 ms against ~1.5–3 ms.
- Thumbnails cost about +14 MB idle footprint per page of real results, mostly decoded pixels in the Go heap. `internal/artwork` keeps a 32 MB memory LRU (images shown in the last two frames stay) and a 128 MB disk cache; MyGo keeps the pixels of about one page's images (~20) after the page is left, until it shows again.
- The Go heap grows to twice what is live before a GC, and the scavenger returns free pages over minutes: `internal/quiet` runs `debug.FreeOSMemory` (0.6–5 ms, off the UI thread) after 3 s without a build, if the heap allocated 1 MB since. With thumbnails: idle ~96 → 77 MB, after every page ~121 → 96 MB, playing minimized ~119 → 93 MB; frames unchanged.

## The background player

Numbers from `scripts/footprint.sh -daemon` (`perf/footprint-daemon-baseline.txt`: M4 Max, a placeholder ytmd, whose engine is a fake on a clock).

- ytmd's footprint: ~6 MB fresh and paused (RSS ~13 MB), ~6.4 MB playing, ~8.5 MB after a Desktop toured every page through it (RSS ~16 MB). A real engine adds about 2 MB of heap while it holds a stream (below) and MyGo's audio output, which closes 5 s into a pause. The window's numbers above hold no player.
- ytmd keeps its queue's songs and, signed in, the library and liked songs (up to 2000 a library part, 5000 liked) for front-ends that connect later. Sources and front-ends decode a copy of each artist, album and cover per song; `catalog.ShareStrings` leaves one of each. Songs shaped as YouTube's (`catalogtest.Decoded`, albums of about three): 5000 liked keep 1.38 MB instead of 1.85 (`TestBudgetLiveLikes`), a queue of 5000 2.1 MB instead of 2.6 (`TestBudgetLiveQueue`, indexes included). Covers, URLs of about 130 bytes, are half of a song. `ytm status`, which status bars may poll, reads the account without copying the likes.
- Paused, ytmd wakes nothing: top counts 0 idle wake-ups and 0 context switches over 10 s, also with a TUI connected (measured by hand), and none while the placeholder plays. No ticker or heartbeat: state goes out on changes only, and the save 2 s after a change, the stream's release a minute into a pause (`internal/playback`, below) and the heap's release 3 s after the last request or timer (`internal/quiet`, as the Desktop) are one-shot timers. Go's runtime alone wakes it, about once a minute (sysmon, for the forced GC).
- Front-ends never poll ytmd ([docs/daemon.md](daemon.md#health)); the TUI itself is not idle (Bubble Tea's renderer ticks at 30 fps while it runs).

## Audio

One engine on every system (`internal/audio`): YouTube's Opus in WebM, fetched in ranges, decoded in Go, played through MyGo's `audio` output. Numbers on an M4 Max.

- Opus over AAC: Opus decoders in pure Go exist under MIT/BSD, AAC's only under the GPL or LGPL, and Opus is at 48 kHz, MyGo's output rate, so nothing resamples. `pion/opus` decodes about 830× faster than real time (`BenchmarkOpusRead`): about 0.1% of a core while a song plays, a 3:33 song in 0.5 s. It matches ffmpeg's sample count exactly and its samples to about 72 dB; a seek decodes the same samples as playing through. `tphakala/go-opus` was 13% faster but fixed point (69 dB, `int16`), `darui3018823/opus` 2.5× slower, allocating per packet.
- Per packet nothing allocates: `webm` reuses one block buffer (about 8 GB/s, `BenchmarkReadFrame`), the decoder its output. A seek makes the decoder's SILK state anew (5 allocs).
- The source keeps a window of 128 KiB chunks: 1 MiB ahead of the reader (about a minute at 128 kbit/s, 30 s at 256) and 256 KiB behind it, for short seeks back. The next request starts once half the read-ahead is left and fills the window: one about every 30 s, none while paused. Chunks leaving the window are reused, so a song of any length allocates about 1.5 MB, once (`BenchmarkSourceRead`), and nothing more as it plays: no garbage piles up between the loop's frees.
- It kept whole songs (up to 8 MiB) so that seeking back cost no request: 6.4 MB of heap 90% through a 6 MB song (5:26 at 150 kbit/s), against 1.1 MB now (an offline replay). A seek out of the window costs one request, backwards as forwards. 2 MiB ahead would halve the requests for 1 MB more; the 15–30 s left when a request starts already outlast a stalled response's 15 s timeout.
- A stream held is about 2 MB: the window, MyGo's second of decoded samples (384 KB) and the decoder (~100 KB). A minute into a pause, or after the last song ended, playback has the engine `Release` it: Now Playing keeps the song, paused, and Play loads it again where it was, through the stream it had (resolved anew if it expires within a minute), showing as loading for the request or two that takes (one for the header, one at the position). The timer runs from the pause, not from the loop's last batch, so browsing paused still releases it; its batch has `quiet` give the memory back 3 s later.
- `quiet` needs no hook while a song plays: nothing posts to the loop then, but the source reuses its chunks and nothing per packet allocates, so streaming allocates about 100 KB per MB (requests, TLS and HTTP/2 frames; an offline replay over HTTP/2), below the 1 MB a free waits for. What a song held goes at its end, with the loop's batch for the next.
- No polling while a song plays: MyGo tells no one when a player ends or runs dry, so a timer fires at the computed end, and another, once the source has waited 200 ms for the network, when what was decoded ahead runs out (MyGo decodes about a second ahead). Only a stall is polled, every 100 ms, until half a second is decoded ahead: a seek's refill, or each piece of a slow response, would otherwise report a stall and its end.

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
- What a build shows per item is made once: a song row's key, label and "artist · album" (`a.rowText`, by song ID), cards' keys and lines (`cardText`), table positions (`numeral`). Funcs handed to MyGo (shortcuts, `OnChange`, `Draw`) are made once too (`a.actions`, `a.paints`, `seekPaint`, `eqPainters`, a theme card's `choose` and `preview`): a closure or method value per build allocates. A list's `ItemKey` and `Rows` stay per build: MyGo calls them after the build, when a leaving page's list may be built too. Together they took steady frames from 57–95 allocations to 11–32.
