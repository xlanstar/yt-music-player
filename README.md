# YT Music Player

A lightweight YouTube Music desktop player. Written in Go with [MyGo](https://mygo.egoist.dev)'s native UI: GPU-drawn, no webview, no Electron. It draws no frames when idle and uses about 60 MB of memory.

Browse Home and Explore, search, open albums and playlists, play from your library and liked songs, and switch between themes or make your own ([docs/theme.md](docs/theme.md)).

> Playback is macOS only for now (AVPlayer). Not affiliated with YouTube or Google.

## Install

With [Homebrew](https://brew.sh), macOS 12 or later:

```sh
brew install --cask xlanstar/tap/yt-music-player
```

Or download from [Releases](../../releases). The app is signed and notarized, and updates itself: it checks this repository's latest release once a day, and on Check for Updates… in the app menu.

## Usage

Requires Go 1.27+.

```sh
go run .                    # run the app
go tool mygo build          # build a .app and .dmg into build/
```

**Sign in:** open the sign-in sheet in the app, copy the `Cookie` request header of any `music.youtube.com` request from your browser's developer tools, and paste it in. It is stored in the macOS Keychain.

**yt-dlp** (optional): if `yt-dlp` is on your `PATH`, it is used as a fallback for songs the built-in clients cannot play.

## Development

```sh
go run -tags placeholder .   # offline fake catalog, signed in, no audio
YT_SOURCE_DELAY=800ms        # add with the above to see loading states
YT_LOG=debug go run .        # explain failed fetches, sign-ins and streams
prek run --all-files         # lint, tests, allocation budgets: must pass
```

- The checks run through [prek](https://github.com/j178/prek); `prek install` adds them as a pre-commit hook. Go, prek and Bun (for the svgo hook) are pinned in `.tool-versions` (asdf, mise).
- Tests run offline against recorded fixtures. Live checks and re-recording are listed in [AGENTS.md](AGENTS.md).
- Releases: `scripts/release.sh minor|patch` and the steps in [docs/release.md](docs/release.md); changes users notice go in [CHANGELOG.md](CHANGELOG.md).
- Code layout and project rules: [AGENTS.md](AGENTS.md). Performance: [docs/performance.md](docs/performance.md) and [docs/benchmarks.md](docs/benchmarks.md).

## License

[MIT](LICENSE)
