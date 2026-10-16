# Changelog

What changes for users, by version ([Semantic Versioning](https://semver.org)). Add each change under Unreleased as it lands; `scripts/release.sh` turns that section into the release's, whose text the GitHub release shows.

## [Unreleased]

### Added

- Linux and Windows downloads, amd64 and arm64: `install.sh` or a Debian package on Linux, a setup on Windows (not code-signed yet, so SmartScreen warns). They update themselves, but for the Debian package, which `apt` updates.

### Changed

- Songs play through the app's own audio engine instead of AVPlayer: YouTube's Opus streams (about 130 kbit/s, 256 where YouTube Music has them) rather than AAC 128k, decoded in Go. It plays on Linux and Windows too, with media keys and the desktop's player controls on Linux (MPRIS).

## [0.1.0] - 2026-10-16

### Added

- First release: Home, Explore, search, albums, playlists, library and liked songs, signed in with a browser cookie kept in the Keychain.
- Playback through AVPlayer, with the queue, shuffle, repeat and volume, and yt-dlp as a fallback.
- Themes, and themes of your own.
- Signed with a Developer ID and notarized: macOS opens it without asking.
- Updates itself: a check once a day, and Check for Updates… in the app menu.
