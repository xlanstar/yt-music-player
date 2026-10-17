# Changelog

What changes for users, by version ([Semantic Versioning](https://semver.org)). Add each change under Unreleased as it lands; `scripts/release.sh` turns that section into the release's, whose text the GitHub release shows.

## [Unreleased]

### Added

- Traditional Chinese: the app, its menus and the update window follow the system's language (or the app's, in System Settings on macOS). Names from YouTube Music stay as YouTube sends them.
- Linux and Windows downloads, amd64 and arm64: `install.sh` or a Debian package on Linux, a setup on Windows (not code-signed yet, so SmartScreen warns). They update themselves, but for the Debian package, which `apt` updates.
- Background playback: music plays on when the window closes or the app quits, in a small background player that the app starts and that sleeps while paused. Quit and Stop Playback, in the app menu, stops the music with the app.
- `ytm`, the player in a terminal (queue, search, your library; no images) and a command line: `ytm status`, `ytm play`, `ytm next`, `ytm volume 50`, `ytm login` to sign in by pasting the cookie, `ytm help` for the rest. Homebrew and the Linux packages put it on the `PATH`; on Windows, turn on Use ytm in a terminal in the settings.
- Start background player at login, in the settings (off by default; `ytm autostart on` too): the media keys and `ytm` work from login, before the app is opened.

### Changed

- After upgrading on macOS, sign in again once: the background player, which now signs in, can't read the sign-in the app kept in the Keychain.
- Songs play through the app's own audio engine instead of AVPlayer: YouTube's Opus streams (about 130 kbit/s, 256 where YouTube Music has them) rather than AAC 128k, decoded in Go. It plays on Linux and Windows too, with media keys and the desktop's player controls on Linux (MPRIS).

### Fixed

- Pages, search and your library that failed to load while offline load again by themselves once the computer is back online, without a click on Retry.
- A song is queued only once: adding a song already queued adds nothing (`ytm` says so), playing it next moves it after the current song, and a playlist that lists a song twice plays it once.

## [0.1.0] - 2026-10-16

### Added

- First release: Home, Explore, search, albums, playlists, library and liked songs, signed in with a browser cookie kept in the Keychain.
- Playback through AVPlayer, with the queue, shuffle, repeat and volume, and yt-dlp as a fallback.
- Themes, and themes of your own.
- Signed with a Developer ID and notarized: macOS opens it without asking.
- Updates itself: a check once a day, and Check for Updates… in the app menu.
