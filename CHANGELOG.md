# Changelog

What changes for users, by version ([Semantic Versioning](https://semver.org)). Add each change under Unreleased as it lands; `scripts/release.sh` turns that section into the release's, whose text the GitHub release shows.

## [Unreleased]

### Added

- Traditional Chinese: the app, its menus and the update window follow the system's language (or the app's, in System Settings on macOS).
- Linux and Windows downloads, amd64 and arm64: `install.sh` or a Debian package on Linux, a setup on Windows (not code-signed yet, so SmartScreen warns). They update themselves; the Debian package updates through `apt`.
- Background playback: music plays on when the window closes or the app quits, in a small background player that sleeps while paused. Quit and Stop Playback, in the app menu, stops it.
- `ytm`, the player in a terminal (queue, search, your library, the mouse; no images) and a command line: `ytm status`, `ytm play`, `ytm next`, `ytm volume 50`, `ytm login`, `ytm help` for the rest. Homebrew and the Linux packages put it on the `PATH`; on Windows, turn on Use ytm in a terminal in Settings. To select text in it, hold Shift (Option in iTerm2, Fn in Terminal) while dragging.
- Start background player at login, in Settings (off by default; or `ytm autostart on`): the media keys and `ytm` work before the app is opened. Deleting the app on macOS or Linux turns it off at the next login; on Windows, turn it off before uninstalling.

### Changed

- After upgrading on macOS, sign in once more: the background player can't read the sign-in the app kept in the Keychain.
- Songs play through the app's own audio engine instead of AVPlayer: YouTube's Opus streams (about 130 kbit/s, 256 where YouTube Music has them) rather than AAC at 128. It plays on Linux and Windows too, with media keys and the desktop's player controls on Linux (MPRIS).

### Fixed

- Pages, search and your library that failed to load while offline load again by themselves once the computer is back online.
- A song is queued only once: adding one already queued adds nothing, playing it next moves it after the current song, and a playlist that lists a song twice plays it once.

## [0.1.0] - 2026-10-16

### Added

- First release: Home, Explore, search, albums, playlists, library and liked songs, signed in with a browser cookie kept in the Keychain.
- Playback through AVPlayer, with the queue, shuffle, repeat and volume, and yt-dlp as a fallback.
- Themes, and themes of your own.
- Signed with a Developer ID and notarized: macOS opens it without asking.
- Updates itself: a check once a day, and Check for Updates… in the app menu.
