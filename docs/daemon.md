# The background player

`ytmd` plays; the Desktop (MyGo) and `ytm` (TUI and CLI) browse and control it over a local socket. Closing a front-end never stops the music. Package docs hold the details: `go doc ./internal/<api|ipc|daemon|remote|…>`.

## Programs

| Binary | Links | Never links (`TestDeps`) |
|---|---|---|
| `ytmd` (`cmd/ytmd`) | `daemon`: `player`, `playback`, `account`, `audio`, `stream`, `auth`, `ytmusic`, `innertube`, MyGo `audio` | MyGo App, `ui`, `i18n`; `locales`, `session`, `loader` |
| `ytm` (`cmd/ytm`) | `remote`, `session`, `loader`, `tui` (Bubble Tea v2, Lip Gloss v2), `locales`, `x/term` | MyGo App, `ui`, `audio`; `audio`, `auth`, `account`, `innertube`, `ytmusic` |
| Desktop (`cmd/desktop`) | MyGo, `remote`, `session`, `loader`, `artwork`, `theme`, `settings` | `audio`, `auth`, `account`, `stream`, `innertube`, `ytmusic` |

All three share `api`, `ipc`, `catalog`, `job`, `notice`, `loop`, `version`, `logfile`. `session` has its own `TestDeps`. The test binaries are exempt: Desktop's tests run a `daemon.Service` in process over `net.Pipe`, on `audio.Fake`.

## Threading

- **ytmd:** one `loop.Loop` goroutine owns the player, playback, account, engine and saved state; it is `playback`'s `post` and `Wake`. Each connection has a reader goroutine, which posts requests to the loop, and a writer goroutine (`ipc.Conn`), which gob-encodes what the loop queued: the loop never waits on a socket and never changes a frame once sent. Catalog requests run on goroutines of their own; their replies come back through the loop.
- **macOS:** `cmd/ytmd` locks `main` to the main thread, which runs `CFRunLoopRun` (`daemon.RunMain`, purego) so the main dispatch queue serves Now Playing; no NSApplication, no window. The engine is made on the loop only once the run loop runs (`go doc ./internal/audio`).
- **Front-ends:** `remote.Client`'s reader hands replies to callers and posts each burst of events once to the front-end's thread (`a.post`; in the TUI a message run in `Update`). The Mirror, `session` and `loader` stay on that thread.

## Paths

`<cfg>` is `os.UserConfigDir()/yt-music-player`, `<cache>` is `os.UserCacheDir()/yt-music-player` (`ipc.Paths`).

| | macOS | Linux | Windows |
|---|---|---|---|
| socket | `<cfg>/ytmd.sock` | `$XDG_RUNTIME_DIR/yt-music-player/ytmd.sock` | `%LOCALAPPDATA%\yt-music-player\ytmd.sock` |
| fallback socket dir | `$TMPDIR/yt-music-player-<uid>/` | `/tmp/yt-music-player-<uid>/` | `%TEMP%\yt-music-player\` |
| lock | `ytmd.lock` beside the socket | same | same |
| state | `<cfg>/daemon.json` | same | same |
| logs | `<cache>/logs/ytmd.log`, `ytmd.1.log`; `ytm.log`, `ytm.1.log` | same | same |
| start at login | `~/Library/LaunchAgents/dev.lanstar.ytmusicplayer.daemon.plist` | `~/.config/systemd/user/ytmd.service` | `HKCU\…\CurrentVersion\Run\YTMusicPlayerDaemon` |

- The fallback holds the socket when Linux has no `$XDG_RUNTIME_DIR`, or when the usual path is longer than `sun_path` (103 bytes on macOS, 107 elsewhere). A socket directory another user owns is refused, in the fallback a symlink too.
- The socket's directory is 0700 and the socket 0600 (chmod, as macOS makes it 0755); only they keep other users out. Windows relies on `%LOCALAPPDATA%`'s ACL.
- **Suffixes:** a placeholder build (`-tags placeholder`) adds `-placeholder` to the socket, lock, state and log names, a development build (`version.Version` is `dev`; Desktop's `mygo.IsDev()`) then `-dev`: `ytmd-dev.sock`, `daemon-placeholder-dev.json`, `ytmd-placeholder-dev.log`. Development front-ends never reach an installed daemon or its files.
- `ytm logs` prints the log paths.

## Starting and stopping

**On demand.** A front-end dials the socket; on any failure it starts ytmd (`ipc.Dialer.DialOrSpawn`), then dials every 50 ms for up to 5 s (`api.ErrDaemonUnavailable`). ytmd is looked for (`ipc.FindDaemon`) at:

1. `$YTMD_PATH` (`scripts/dev.sh` sets it);
2. beside the program, symlinks resolved: `ytmd` (`ytmd.exe`) for Linux and Windows; on macOS `YT Music Player Helper.app/Contents/MacOS/ytmd` (ytm, in `Contents/Resources`), then `../Resources/YT Music Player Helper.app/Contents/MacOS/ytmd` (Desktop, in `Contents/MacOS`);
3. else `api.ErrDaemonNotFound`, which names `YTMD_PATH`.

It starts detached (`Setsid` on Unix; `CREATE_NEW_PROCESS_GROUP | DETACHED_PROCESS` on Windows, where ytmd is built `-H=windowsgui`), with `/dev/null` for its streams, the front-end's environment (`YT_LOG` too) and ytmd's directory as its working directory.

**Single instance.** ytmd makes its directories, takes the lock (`flock`, `LockFileEx`; never waiting) or exits 0 when another holds it, loads `daemon.json`, then listens, removing a stale socket first, so the first snapshot is whole. Then it resumes after a hand-off, fetches the account, library and likes, and accepts connections. The lock file stays; the OS releases the lock with the process.

**Stopping.** Only `ytm daemon stop`, Desktop's *Quit and Stop Playback* (app menu), a `Shutdown` request, SIGTERM or SIGINT stop ytmd: it pauses, saves, closes the engine and media controls, stops listening (removing the socket), flushes its connections for up to 1 s and releases the lock. It never exits for being idle. Windows has no SIGTERM, and a `windowsgui` process is killed at logout: the queue and settings were saved within 2 s, the position at the last pause, seek or new song.

## Versions and hand-off

Each side's first frame is a `Hello{Proto, Version, Role, Subscribe, PID}`; ytmd answers with its own (`RoleDaemon`). Versions are semver (`version.Version`, set by `-ldflags -X` from `mygo.json`; Desktop's `App.Version()`); `dev` is never compared. Peers of another `Proto` may only `Ping`, `Handoff` and `Shutdown` (others get `Internal`) and get no events but `HandoffEvent`.

The Desktop and the TUI run a `remote.Supervisor`:

- **Daemon older:** hand off to the front-end's own ytmd, once per daemon version per front-end; still older (an old `YTMD_PATH`, two installs), the health is `DaemonOutdated`: calls go on if the Proto matches, else stop.
- **Daemon newer:** nothing, if the Proto matches; else `FrontendOutdated`, and calls stop.
- **Hand-off:** the front-end calls `Handoff{Wait: 5s}`. ytmd tells its other connections `HandoffEvent{By: <pid>, Wait}`, so they only redial, never start a daemon, for `Wait`; replies; saves with `resumePlaying` (it was playing) and `resumeBefore` (now + `Wait` + 30 s); and stops, answering nothing more. The front-end waits for it to go, starts its own ytmd, and dials it for `Wait`. That ytmd plays on if `resumePlaying` is set and not expired, and clears it; any other start is paused. `ytm daemon restart` hands off the same way.
- Updates need nothing more: the updater renames or replaces files, and a running ytmd keeps its own; the relaunched Desktop hands off.

The CLI connects once (`remote.Connect`), never subscribes and never hands off: an older daemon gets a warning on stderr. Controls, `seek`, `volume`, `login`, `logout`, `daemon start` and `daemon restart` start ytmd; `status`, `daemon status`, `daemon stop`, `version`, `logs` and `autostart` never do. Exit codes: 0, 1 failed, 2 usage, 3 not running (`status`, `daemon status`). `ytm status --json`'s fields are held by `cmd/ytm/testdata/status.json`.

## Health

`remote.Link.Health`, shown as Desktop's banner and the TUI's status line (words from `locales`, never a notice): `Connecting`, `Connected`, `Reconnecting`, `Unresponsive`, `Unavailable`, `DaemonOutdated`, `FrontendOutdated`. Nothing runs on a timer while connected; the daemon is checked only:

- on connecting (Hello, versions);
- when the connection ends: calls waiting fail with `api.ErrDisconnected`, and the Supervisor redials, starting ytmd, after 0.25 s doubling up to 30 s, for as long as the front-end runs; 3 failures in a row show `Unavailable` (Retry, View Log). Retry or coming to the foreground redials now from 0.25 s;
- with a `Ping` when the front-end comes to the foreground or a control gets no reply within 2 s (the control is told as `ControlFailed`): no reply in 2 s is `Unresponsive` (Restart: `Shutdown`, then kill the Hello's PID after 3 s); any frame makes it `Connected` again.

The first events after reconnecting are a full snapshot, which replaces the front-end's Mirror.

## Protocol

AF_UNIX stream sockets on every OS (Windows 10 1803+), one long-lived gob encoder and decoder per direction, no length prefix; the writer flushes once per batch. The receiver zeroes its `Frame` before each decode, as gob omits zero values.

```go
const Proto uint16 = 1
type Frame struct { ID uint32; Hello *Hello; Call *Call; Reply *Reply; Cancel bool; Event *Event }
```

- **Requests:** a `Call{Method, <one args pointer>}` with `ID` from 1; its `Reply{Err | one result}` carries the same `ID`. `Frame{ID, Cancel: true}` cancels a catalog request or the `SignIn` in flight, as a front-end's context ends; a closed connection cancels all of its requests. The front-end returns `ctx.Err()` without waiting for a reply.
- **Methods** (`api.Method`, numbered from 1, only appended):
  - bootstrap: `Ping`, `Handoff(Wait)`, `Shutdown`; `Status` (`ytm status`'s snapshot);
  - playback: `Play(IDs, Start)` (as Desktop's rows: resume or pause the current song, jump to a queued one, else queue it next), `PlayAll(IDs, Start)` (the IDs become the queue), `Toggle`, `Pause`, `Resume`, `Next`, `Prev`, `Seek(Seconds)`, `SetVolume(0–1)`, `ToggleMute`, `CycleRepeat`, `ToggleShuffle`;
  - queue: `Enqueue(IDs, Next)`, `Remove(Index)`, `Move(From, To)`, `ClearUpNext`. `IDsArgs.Songs` carry the songs' data: ytmd keeps no catalog but its queue's songs;
  - catalog (`catalog.Source`): `Search`, `Album`, `Playlist`, `Home`, `Explore`, `Account`, `Library`; ytmd caches no pages;
  - account (`catalog.Accounts`): `Like(ID, Liked, Song)`, where the optional `Song` lets ytmd add a song it never sent to the likes' data; `SignIn(Credentials)` (never logged; a newer sign-in replaces one in flight); `SignOut`; `Refresh(Part)` fetches the profile, library or likes anew.
- **Events** go to connections whose Hello subscribed (Desktop, TUI); several fields may be set:
  - first a snapshot: the `State`, the `Queue` and all its `Songs`, then the account, library and likes as far as fetched, then one `Failed` per part whose last fetch failed;
  - `State{Playing, Waiting, Position, Duration, Volume, Muted, Repeat, Shuffled, Current, Index}`, whole, on play, pause, a new song, a stall's start or end, volume, mute, repeat, shuffle, or a jump of the position by more than 1 s from where the last State put it (a seek); never on a timer. Receivers move `Position` on with their own monotonic clock while playing and not waiting; no `time.Time` crosses the socket;
  - `Queue{IDs}` in play order (shuffled when shuffling), whole, with the `Songs` new to it;
  - `Likes{Full, IDs, Songs, Added, Removed}`: `Full` with every liked song's data at first, then deltas, whose `Songs` are the added songs' data; never coalesced;
  - `Account{Account, SignedIn, Switched}`: `Switched` is a sign-in, after which what was fetched is stale, and ytmd empties the queue (the previous account's); `Library`; `Failed{Part, Err}` (Retry sends `Refresh`); `Notice`; `Handoff`.
- **Order:** a request's reply precedes the events it causes (a `Like`'s `SignedOut` before the sign-out's `Account`), but for `SignIn` and `SignOut`, whose `Account` event comes first, as package `account` orders them.
- **Backpressure:** each connection queues up to 64 frames for its writer; a newer `State` replaces one still queued, going last; anything else is kept. A full queue closes the connection, and the front-end reconnects to a snapshot.
- **Errors:** `api.Encode` sends a code, `remote.Decode` makes it an error whose `errors.Is` finds the sentinel and whose text is the sentinel's, then `Detail`:

  | Code | Sentinel | Detail |
  |---|---|---|
  | `NotFound` | `catalog.ErrNotFound` | none |
  | `SignedOut` | `catalog.ErrSignedOut` | none |
  | `InvalidCredentials` | `catalog.ErrInvalidCredentials` | the reason, which the sign-in form shows |
  | `Internal` | `api.ErrInternal`: anything else, or a code unknown to the receiver | none; ytmd logs the error at debug |

  Only front-ends make `ErrDaemonNotFound`, `ErrDaemonUnavailable`, `ErrDisconnected` and `ErrProtoMismatch`. A new `errors.Is` on a daemon error in a front-end needs a code (`TestErrorCodes`); `Detail` never holds cookies, headers, visitorData or stream URLs.
- **Notices** are `notice.Notice{Kind, Title, Detail Code, Args, N, Topic}`, coded, never worded: front-ends say them through `tell` (`noticetext`), so ytmd links no `locales`. Their `Kind`s and `Code`s travel as numbers: only append them (before `NumCodes`).

### Rules for the wire

- Every wire enum (`Method`, `Role`, `Part`, `Repeat`, `ErrorCode`) starts at 1: a 0 is a missing field, rejected (`Validate`).
- No `interface{}`, func or chan fields, no `gob.Register`; `catalog`'s plain structs travel as they are (`TestWireTypes`).
- Adding a field needs no new Proto: gob skips what the receiver lacks. Changing a field's meaning needs one. **Never change a field's type**, which makes gob refuse every frame, the bootstrap ones too: give the new type a new name.
- **Frozen bootstrap:** `Frame.ID`, `Hello`, `Reply.Err`, `Error`, `HandoffArgs`, `HandoffEvent`, and the numbers of `Ping` (1), `Handoff` (2) and `Shutdown` (3) never change in any Proto (`TestBootstrapFrozen` reads a Proto 1 stream), so a front-end can always hand off to, or stop, any daemon.

## Saved state

`<cfg>/daemon.json`, encoding/json/v2 defaults, written to a temporary file renamed over it:

```json
{"v":1,"queue":["id",…],"index":3,"order":[…],"position":81.5,"volume":0.7,"muted":false,"repeat":0,"shuffled":false,
 "songs":[{"id":"…","title":"…","artist":"…","album":"…","albumId":"…","seconds":213,"art":"…"}],
 "resumePlaying":true,"resumeBefore":"…"}
```

- `order` is the play order (shuffled) as indexes into `queue`; `index` is in play order; `repeat` is 0 off, 1 all, 2 one; `songs` are the queue's songs, zero fields left out, as ytmd has no catalog to describe them otherwise. The `resume…` fields come only from a hand-off.
- The queue and settings save 2 s after a change (a burst saves once); the position only on a pause, seek, new song, stop or hand-off, never on a timer while playing.
- A file that fails to read, or of another `v`, is renamed `daemon.json.bad`, and ytmd starts empty; with none, it starts from the build's seed (the placeholder's queue), paused.

## Logs

ytmd and ytm log through `logfile.Writer` to `<cache>/logs/` (ytm owns the terminal): 1 MiB a file, then it becomes `<name>.1.log`, replacing the one before. `YT_LOG` sets the level (default `warn`); a front-end passes its own to the ytmd it starts, while one started at login logs at `warn`: `YT_LOG=debug ytm daemon restart` changes it. `AGENTS.md`'s logging rules hold.

## Start at login

Off by default; Desktop's settings (*Start background player at login*) or `ytm autostart on|off|status` (package `autostart`). Switching decides only the next login, never starts or stops ytmd now. Desktop and ytm repair the entry's path at start, as the app may move.

- **macOS:** the LaunchAgent above (`RunAtLoad`, `LimitLoadToSessionType=Aqua`, `ProcessType=Interactive`, no `KeepAlive`, which would restart a stopped ytmd), `launchctl enable|disable gui/<uid>/<label>`, never `bootstrap` or `bootout`. Its `ProgramArguments` are `/bin/sh`, `-c`, `[ -x "$0" ] && exec "$0"; rm -f '<plist>'; launchctl disable gui/$(id -u)/<label>` and `<ytmd>`: ytmd as before while it exists, else the agent removes its plist and disables its label, so deleting the app (brew, the Trash, anything) turns starting at login off at the next login. ytmd is `$0`, never parsed as shell; the plist is its absolute path, single-quoted. `AssociatedBundleIdentifiers` (the app's) has Login Items show *YT Music Player*, not `sh` (macOS 13+). Repair rewrites a plist of the older format, which ran ytmd directly.
- **Linux:** the user unit (`ConditionPathExists=<ytmd>`, `Restart=no`, `WantedBy=default.target`), `systemctl --user enable|disable` without `--now`. With ytmd gone, systemd skips the unit (no failure); the file stays.
- **Windows:** the Run value, `"<ytmd.exe>"`, which cannot clean itself up: turn it off before uninstalling. Desktop's *Use ytm in a terminal* (Windows only, off by default) adds the app's folder to `HKCU\Environment`'s `Path`, keeping its type, and broadcasts `WM_SETTINGCHANGE`.

Accepted trade-off: an app on a disk not mounted at login (an external drive) reads as deleted, and macOS turns starting at login off; turn it on again.

## Packaging

`scripts/build-helpers.sh <platform>…` builds ytmd and ytm into `resources/<platform>/` before `mygo build`, which ships, signs and (universal) merges them (`docs/release.md`):

- **macOS:** `Contents/Resources/YT Music Player Helper.app` (`ytmd`, `dev.lanstar.ytmusicplayer.daemon`, `LSUIElement`, the app's icon; Now Playing shows the folder's name, which is not the app's so that nothing finds two *YT Music Player*s) and `Contents/Resources/ytm`, which the cask links into Homebrew's `bin`; `brew uninstall` runs `ytm daemon stop` first and leaves the LaunchAgent, so that upgrades keep it: without the app, it removes itself at the next login (`--zap` removes it at once).
- **Linux:** `ytmd`, `ytm` and the `ytmd.service` template beside the app; `linux.command` puts `ytm` on the `PATH` as a link to the app, which runs the `ytm` beside it under that name.
- **Windows:** `ytmd.exe` (no console) and `ytm.exe` beside the app. The installer cannot stop ytmd or remove the Run value and `Path` entry: before uninstalling, choose *Quit and Stop Playback* and turn both switches off.

## Known limitations

- Windows has no media keys (SMTC).
- The TUI is not free of wake-ups: Bubble Tea v2.1's renderer runs a 30 fps frame timer while it is open (it writes nothing when frames match). On Windows its input loop also polls every 10 ms while idle ([ultraviolet #216](https://github.com/charmbracelet/ultraviolet/issues/216)). ytmd and Desktop wake nothing while paused (`scripts/footprint.sh -daemon`).
- On macOS, the sign-in must be made again once after upgrading: the Keychain item is the Desktop's, which ytmd, signed as another program, cannot read. Linux and Windows keep credentials in a file under `<cfg>`.
- A sign-in that switches accounts clears the queue.
- No images in the TUI; no remote or network control.

Still to check on real installs:

- headphone and Bluetooth media buttons (keyboard media keys work);
- Linux playback on real PulseAudio or PipeWire; the whole Windows path, `-H=windowsgui` and windowless WASAPI included;
- AF_UNIX on Windows 10 and 11, with a user name in Chinese in the path;
- updating Windows while `ytmd.exe` plays and `ytm.exe` runs, and running the NSIS installer again then;
- `YT Music Player Helper.app` absent from Spotlight and Launchpad, its name and icon in Now Playing; App Nap of a paused ytmd started by launchd;
- focus, Ctrl+C, arrow keys, leaving the alt screen and the cursor in Windows Terminal and conhost.
