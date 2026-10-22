# The background player

`ytmd` plays; the Desktop and `ytm` (TUI and CLI) browse and control it over a local socket, so closing a front-end never stops the music. This page is the design; package docs hold the details (`go doc ./internal/api`, `ipc`, `daemon`, `remote`, `autostart`).

## Programs

| Binary | Links | Never links |
|---|---|---|
| `ytmd` (`cmd/ytmd`) | `daemon`, and what plays and signs in: `player`, `playback`, `account`, `audio`, `stream`, `auth`, `ytmusic`, MyGo's `audio` | MyGo's App, `ui`, `i18n`; `locales`, `session`, `loader`; pprof, but in `memprof` builds |
| `ytm` (`cmd/ytm`) | `remote`, `session`, `loader`, `tui` (Bubble Tea, Lip Gloss), `termimage`, `artwork`, `locales` | MyGo's App, `ui`, `audio`; `audio`, `auth`, `account`, `innertube`, `ytmusic`; x/image's `draw` |
| Desktop (`cmd/desktop`) | MyGo, `remote`, `session`, `loader`, `artwork`, `theme`, `settings` | `audio`, `auth`, `account`, `stream`, `innertube`, `ytmusic` |

Each binary's `TestDeps` (and `session`'s) holds the last column; the test binaries are exempt, as Desktop's tests run a daemon in process. All three share `api`, `ipc`, `catalog`, `job`, `notice`, `version` and `logfile`.

## Threading

- **ytmd:** one loop goroutine (package `loop`) owns the player, playback, account, engine and saved state. Each connection has a reader goroutine, which posts requests to the loop, and a writer goroutine, which encodes what the loop queued: the loop never waits on a socket. Catalog requests run on goroutines of their own; their replies come back through the loop.
- **macOS:** ytmd's main thread runs the Core Foundation run loop (no NSApplication, no window), so the main dispatch queue serves Now Playing; the engine is made only once it runs.
- **Front-ends:** the client's reader hands replies to callers and posts each burst of events once to the front-end's thread (the TUI's `Update`), where the playback mirror, `session` and `loader` live.

## Paths

`<cfg>` is `os.UserConfigDir()/yt-music-player`, `<cache>` is `os.UserCacheDir()/yt-music-player`.

| | macOS | Linux | Windows |
|---|---|---|---|
| socket, and `ytmd.lock` beside it | `<cfg>/ytmd.sock` | `$XDG_RUNTIME_DIR/yt-music-player/ytmd.sock` | `%LOCALAPPDATA%\yt-music-player\ytmd.sock` |
| fallback socket dir | `$TMPDIR/yt-music-player-<uid>/` | `/tmp/yt-music-player-<uid>/` | `%TEMP%\yt-music-player\` |
| state | `<cfg>/daemon.json` | same | same |
| logs | `<cache>/logs/ytmd.log`, `ytm.log` | same | same |
| library and likes, the front-ends' | `<cache>/account.json` | same | same |
| start at login | `~/Library/LaunchAgents/dev.lanstar.ytmusicplayer.daemon.plist` | `~/.config/systemd/user/ytmd.service` | `HKCU\…\CurrentVersion\Run\YTMusicPlayerDaemon` |

- The fallback holds the socket when Linux has no `$XDG_RUNTIME_DIR` or the usual path is too long for `sun_path` (103 bytes on macOS, 107 elsewhere).
- Only the socket's directory (0700, refused when another user owns it) and the socket (0600) keep other users out; Windows relies on `%LOCALAPPDATA%`'s ACL.
- A placeholder build adds `-placeholder` to the socket, lock, state, log and cache names, a development build (version `dev`) then `-dev` (`daemon-placeholder-dev.json`), so development front-ends never reach an installed daemon or its files. `ytm logs` prints the log paths.

## Starting and stopping

**On demand.** A front-end that cannot connect, whatever the failure, starts ytmd, then dials every 50 ms for up to 5 s. It looks for ytmd at `$YTMD_PATH`, else beside itself, symlinks resolved: `ytmd` (`ytmd.exe`), on macOS in `Contents/Resources/YT Music Player Helper.app`. ytmd starts detached, its streams on the null device, in its own directory, with the front-end's environment.

**Single instance.** ytmd takes the lock without waiting, or exits 0 when another holds it; loads `daemon.json`; listens, removing a stale socket; resumes after a hand-off; fetches who is signed in (`ytm status` shows it), again for a subscriber after a failure; front-ends fetch the library and likes through it. Stopping releases the lock, else the OS does.

**Stopping.** Only `ytm daemon stop`, Desktop's *Quit and Stop Playback*, a `Shutdown` request, SIGTERM or SIGINT stop ytmd, never idleness: it pauses, saves, closes the engine, removes the socket, unlocks, then flushes its connections for up to 1 s. Windows has no SIGTERM and kills a windowless process at logout, losing only what [Saved state](#saved-state) had not yet written.

## Versions and hand-off

Each side's first frame is a Hello with its protocol number (`api.Proto`), version and PID. Versions are MAJOR.MINOR.PATCH from `mygo.json` (`-ldflags -X`); `dev` is never compared. Peers of another protocol may only ping, hand off and shut down, and get no events but the hand-off's.

The Desktop and the TUI:

- **Daemon older:** hand off to the front-end's own ytmd, once per daemon version. Still older (an old `YTMD_PATH`, two installs): the health is *DaemonOutdated*; calls go on if the protocol matches, else stop.
- **Daemon newer:** nothing if the protocol matches; else *FrontendOutdated*, and calls stop.
- **Hand-off:** asked with a 5 s wait, ytmd tells its other connections to only redial meanwhile, never start a daemon; replies; saves whether it was playing, valid for the wait plus 30 s; and exits. The asking front-end starts its own ytmd, which plays on only if that note, still valid, says so. `ytm daemon restart` hands off alike.
- **Updates** need nothing more: a running ytmd keeps its files, and the relaunched Desktop hands off.

The CLI connects once, never subscribes to events and never hands off: an older daemon gets a warning on stderr. Controls, `login`, `logout`, `daemon start` and `daemon restart` start ytmd; `status`, `daemon status`, `daemon stop`, `version`, `logs` and `autostart` never do. Exit codes: 0, 1 failed, 2 usage, 3 not running (`status`, `daemon status`). `cmd/ytm/testdata/status.json` holds `ytm status --json`'s fields.

## Health

Shown as Desktop's banner and the TUI's status line (words from `locales`, never a notice): *Connecting*, *Connected*, *Reconnecting*, *Unresponsive*, *Unavailable*, *DaemonOutdated*, *FrontendOutdated*. Nothing runs on a timer while connected; the daemon is checked only:

- on connecting (Hello, versions);
- when the connection ends: waiting calls fail, and the front-end redials, starting ytmd, after 0.25 s doubling up to 30 s, for as long as it runs; 3 failures in a row are *Unavailable* (Retry, View Log). Retry or coming to the foreground redials at once;
- with a ping when the front-end comes to the foreground or a control gets no reply within 2 s: no reply in 2 s is *Unresponsive* (Restart: ask to shut down, kill after 3 s, start another); any frame makes it *Connected* again.

After reconnecting, the first events are a full snapshot, which replaces the front-end's mirror.

## Protocol

AF_UNIX stream sockets on every OS (Windows 10 1803+), one gob stream per direction. `go doc ./internal/api` has the frames, methods, events and the rules for changing them (only append enum values; never change a field's type; the bootstrap messages, which let any front-end hand off to or stop any daemon, never change).

- **Requests** carry an ID; the reply carries it back. A front-end whose context ends sends a cancel (catalog requests, sign-in) and returns at once; a closed connection cancels all its requests. A method of the Proto that ytmd serves nowhere fails as internal, never as done (`TestEveryMethodServed`).
- **Data:** ytmd keeps no catalog, library or likes and caches no pages, only its queue's songs, which front-ends send with the IDs they play or queue. The queue holds each song once (package `player`). A catalog request answered signed out signs ytmd out, as its own do.
- **Events:** first a snapshot (state, queue and its songs, who is signed in or why that failed), then changes, likes that went through among them. The playback state goes out whole when a field changes or the position jumps over 1 s, never on a timer: receivers move the position with their own clock. Switching accounts empties the queue.
- **Accounts:** who is signed in comes with the sign-in's ID, random, made by ytmd at each sign-in and stored with the credentials: a restart keeps it, a sign-out forgets it. Front-ends tell accounts, and their cache of the library and likes, by it: names and handles repeat and change.
- **Long lists** go in frames of 256 items, or more so that a list takes about 16 (`api.PartSize`): gob keeps a sender's buffer as large as its largest frame, a reader the last frame. An event's songs go ahead in events of songs; a call's songs and a reply's lists in frames of its ID holding them alone (`api.Frame.Ahead`).
- **Parts** wait for their call in ytmd's reader, 128 at most a connection, more closing it, and for their reply in the front-end's; a closed connection drops them. A call in parts canceled still goes whole, its parts first.
- **Order:** a reply precedes the events its request causes, but for sign-in and sign-out, whose account event comes first.
- **Backpressure:** each connection queues up to 128 frames, replies' too: twice a front-end's most at once (a queue's songs, the library, Liked music, a playlist). A newer playback state replaces a queued one. A full queue closes the connection, and the front-end reconnects to a snapshot.
- **Errors** travel as codes: not found, signed out, invalid credentials (with the reason, which the sign-in form shows) and internal (anything else, logged by ytmd at debug). A front-end that needs `errors.Is` on a new daemon error adds a code (`TestErrorCodes`). Nothing on the wire holds cookies, headers, visitorData or stream URLs.
- **Notices** travel as codes and arguments; front-ends word them (`noticetext`), so ytmd links no `locales`.

## Saved state

`<cfg>/daemon.json`: the queue and its songs, play order, index, position, volume, mute, repeat, shuffle, and a hand-off's resume note (package `daemon`). Written whole (package `atomicfile`).

- The queue and settings save 2 s after a change (a burst saves once); the position only on a pause, seek, new song, stop or hand-off.
- A file that fails to read, or of another version, becomes `daemon.json.bad`, and ytmd starts empty; with none, from the build's seed (the placeholder's queue), paused.

## Logs

ytmd and ytm log to `<cache>/logs/` (ytm's TUI owns the terminal): 1 MiB a file, then it becomes `<name>.1.log`, replacing the one before. `YT_LOG` sets the level (default `warn`); a front-end passes its own to the ytmd it starts, while one started at login logs at `warn`: `YT_LOG=debug ytm daemon restart` changes it.

## Start at login

Off by default; Desktop's settings (*Start background player at login*) or `ytm autostart on|off|status`. Switching decides only the next login, never starts or stops ytmd now. Desktop and ytm repair the entry's path at start, as the app may move.

- **macOS:** a LaunchAgent without `KeepAlive` (which would restart a stopped ytmd), switched with `launchctl enable|disable`. It runs ytmd through `/bin/sh`, which, once ytmd is gone, removes the plist and disables the label: deleting the app turns starting at login off at the next login. Login Items shows *YT Music Player* (macOS 13+).
- **Linux:** a systemd user unit (`Restart=no`), `systemctl --user enable|disable` without `--now`; with ytmd gone, systemd skips it.
- **Windows:** a Run value, which cannot clean itself up: turn it off before uninstalling. Desktop's *Use ytm in a terminal* (Windows only, off by default) adds the app's folder to the user's `Path`.

An app on a disk not mounted at login reads as deleted, and macOS turns starting at login off: turn it on again.

## Packaging

`scripts/build.sh` builds ytmd and ytm, then the app with `mygo build`, which ships them beside it; where each lands, and what the installers do with them: [docs/release.md](release.md#the-helpers).

## Known limitations

- Windows has no media keys (SMTC).
- The TUI wakes while open: Bubble Tea v2.1's renderer runs a 30 fps frame timer (writing nothing when frames match), and on Windows its input loop polls every 10 ms ([ultraviolet #216](https://github.com/charmbracelet/ultraviolet/issues/216)). A paused ytmd and the Desktop wake nothing (`scripts/footprint.sh -daemon`).
- On macOS, sign in once more after upgrading from 0.1: ytmd, signed as another program, cannot read the Desktop's Keychain item. Linux and Windows keep credentials in a file under `<cfg>`.
- No remote or network control.
- The TUI's covers take, in order, Kitty's Unicode placeholders (kitty, Ghostty, iTerm2 from 3.7.5 by its XTVERSION; with 256 colors, whose index names the image), iTerm2's inline images (WezTerm, older iTerm2), Sixel or 256 colors; `YTM_IMAGES=off|blocks|sixel|iterm2|kitty` overrides. In tmux, Kitty's need `allow-passthrough on` and iTerm2's are not used; tmux answers XTVERSION, so iTerm2's placeholders are not either.
- The TUI takes the mouse, so terminals select text only with a modifier held while dragging: Shift in most, Option in iTerm2, Fn in Terminal.app.

Still to check on real installs:

- headphone and Bluetooth media buttons (keyboard media keys work);
- Linux playback on real PulseAudio or PipeWire; the whole Windows path, windowless ytmd and WASAPI included;
- AF_UNIX on Windows 10 and 11, with a Chinese user name in the path;
- updating Windows while `ytmd.exe` plays and `ytm.exe` runs, and running the installer again then;
- `YT Music Player Helper.app` absent from Spotlight and Launchpad, its name and icon in Now Playing; App Nap of a paused ytmd started by launchd;
- focus, Ctrl+C, arrow keys, leaving the alt screen and the cursor in Windows Terminal and conhost.
