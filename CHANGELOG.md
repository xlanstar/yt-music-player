# Changelog

What changes for users, by version ([Semantic Versioning](https://semver.org)). Add each change under Unreleased as it lands; `scripts/release.sh` turns that section into the release's, whose text the GitHub release shows.

## [Unreleased]

### Changed

- A mouse wheel scrolls pages smoothly, as browsers do, rather than jumping a notch at a time; a touchpad still follows your fingers.
- The app checks for updates shortly after each launch, then once a day, so a version released since you last opened it shows at once.
- The window redraws only what changed, using less GPU while a song plays; on macOS it draws nothing while other windows cover it entirely.
- On Linux, the `.deb` no longer depends on WebKitGTK, and `install.sh` no longer warns about it: the app never used it.
- On Windows 11, the menus look like the system's own.
- On Windows, Settings > Apps names xlanstar as the app's publisher.
- On macOS, swiping between pages on a touchpad, or with a mouse that sends swipes, goes back and forward.
- The background player keeps a quarter of a second of sound ready rather than 40 ms, so a song plays on while the system briefly holds it up; pausing and seeking are heard that much later.

### Fixed

- A song row that scrolls under the pointer highlights at once, without moving the pointer.
- On macOS, the window no longer shakes while you drag it by its top bar, and the app no longer starts the AutoFill helper, which used CPU and could stall scrolling.
- On macOS, an update keeps the app's single Dock tile, and one found in the background shows in front of the window, or once the app comes to the front.
- Quit (⌘Q on macOS) always closes the app.
- On Windows, a hidden or minimized window no longer stalls the app for a second each frame, and with accent colors on title bars no colored strip shows at the window's top.
- On Linux, a quick touchpad tap no longer leaves the pointer stuck pressed.

## [0.3.0] - 2026-10-23

### Changed

- The app and `ytm` show your library and liked songs as soon as they open, from a copy kept since last time, while fetching them anew. Signing out, or in to another account, deletes the copy.
- The app and `ytm` download covers as WebP, about a third smaller at the same sharpness, so pages fill sooner and the cover cache holds more.
- In `ytm`, iTerm2 3.7.5 and later show covers as Kitty's placeholders, text the terminal keeps, so covers are never redrawn and never vanish.
- The background player uses less memory: it no longer keeps your library and liked songs (1.3 MB with 5000 liked), which the app and `ytm` fetch through it, and, for each open window or `ytm`, keeps about 1.3 MB less once it showed a list of thousands of songs (the queue, Liked music, a playlist), and 1.1 MB less once it played one.
- After updating while the background player of 0.2.0 still runs (started at login, say), `ytm`'s commands, such as `ytm play` or `ytm status`, refuse to work, saying so, until the app or the `ytm` player opens or `ytm daemon restart` runs.

### Fixed

- Built with Go 1.27.3, which distrusts compromised intermediate certificates (GO-2026-6809) on every connection to YouTube.
- A seek from the system's media controls, such as Now Playing on macOS, is checked as the app's own are: one before the start plays from the start, and one with nothing queued or without a position is ignored, rather than leaving the background player at a position it can't save or send to the app.
- A media key pressed just as the app or `ytm` changed the song no longer starts the new song where the old one was.
- The app says songs of an hour or more as 1:15:12 rather than 75:12, and an album’s or playlist’s length in minutes rounded to the nearest, as `ytm` does.
- The background player's memory no longer grows with every song played: it kept about half a megabyte of each until it stopped.
- After an update, music keeps playing even while another window or `ytm` is slow to let go of the old background player.
- In `ytm`, covers in iTerm2, WezTerm and Sixel terminals no longer vanish beside wide characters, such as a Japanese song title, a moment after showing.
- In `ytm`, the search box types at the terminal's own cursor, so input methods (Chinese, Japanese) show their candidates beside it rather than elsewhere on the screen.
- An empty playlist or album no longer says the songs you like will show up there; only Liked music does.
- In `ytm`, rows whose titles hold emoji such as 👩‍💻, 👍🏽 or ❤️ no longer run past the window's edge or out of line with the others in terminals that count such emoji by character, such as Terminal or any over SSH.
- On Linux, the desktop's player controls can no longer seek past the end of a song: a position past its length does nothing, as MPRIS asks, and a seek forward stops at the end.

## [0.2.0] - 2026-10-19

### Added

- Traditional Chinese: the app, its menus and the update window follow the system's language (or the app's, in System Settings on macOS).
- Linux and Windows downloads, amd64 and arm64: `install.sh` or a Debian package on Linux, a setup on Windows (not code-signed yet, so SmartScreen warns). They update themselves; the Debian package updates through `apt`.
- Background playback: music plays on when the window closes or the app quits, in a small background player that sleeps while paused, and lets go of the song's memory a minute into a pause (playing again then takes a moment). Quit and Stop Playback, in the app menu, stops it.
- `ytm`, the player in a terminal (queue, search, your library, the mouse, and covers in terminals that show images: Kitty, Ghostty, iTerm2, WezTerm, Sixel, or colored blocks; `YTM_IMAGES=off` turns them off) and a command line: `ytm status`, `ytm play`, `ytm next`, `ytm volume 50`, `ytm login`, `ytm help` for the rest. Homebrew and the Linux packages put it on the `PATH`; on Windows, turn on Use ytm in a terminal in Settings. Backspace takes the selected song out of its queue. To select text in it, hold Shift (Option in iTerm2, Fn in Terminal) while dragging.
- Start background player at login, in Settings (off by default; or `ytm autostart on`): the media keys and `ytm` work before the app is opened. Deleting the app on macOS or Linux turns it off at the next login; on Windows, turn it off before uninstalling.

### Changed

- After upgrading on macOS, sign in once more: the background player can't read the sign-in the app kept in the Keychain.
- Songs play through the app's own audio engine instead of AVPlayer: YouTube's Opus streams (about 130 kbit/s, 256 where YouTube Music has them) rather than AAC at 128. It plays on Linux and Windows too, with media keys and the desktop's player controls on Linux (MPRIS).

### Fixed

- Pages, search and your library that failed to load while offline load again by themselves once the computer is back online.
- yt-dlp installed with Homebrew or pipx is found by the background player, so songs that need it play.
- A song is queued only once: adding one already queued adds nothing, playing it next moves it after the current song, and a playlist that lists a song twice plays it once.

## [0.1.0] - 2026-10-16

### Added

- First release: Home, Explore, search, albums, playlists, library and liked songs, signed in with a browser cookie kept in the Keychain.
- Playback through AVPlayer, with the queue, shuffle, repeat and volume, and yt-dlp as a fallback.
- Themes, and themes of your own.
- Signed with a Developer ID and notarized: macOS opens it without asking.
- Updates itself: a check once a day, and Check for Updates… in the app menu.
