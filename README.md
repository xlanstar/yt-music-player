# YT Music Player

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/images/readme-hero-dark.jpg">
  <img src="docs/images/readme-hero-light.jpg" alt="YT Music Player's home page, a song playing and its queue beside it">
</picture>

A lightweight YouTube Music desktop player for macOS, Linux and Windows. Native, GPU-drawn UI in Go with [MyGo](https://mygo.egoist.dev): no webview, no Electron, about 60 MB of memory, no frames drawn when idle.

- Home, Explore, search, albums, playlists, your library and liked songs
- Queue, shuffle, repeat, media keys
- Themes, or [your own](docs/theme.md)

> Not affiliated with YouTube or Google.

## Install

| System | How |
|---|---|
| macOS 12+ | `brew install --cask xlanstar/tap/yt-music-player`, or the `.dmg` |
| Linux (amd64, arm64) | `curl -fsSL https://github.com/xlanstar/yt-music-player/releases/latest/download/install.sh \| sh`, or the `.deb` |
| Windows 10+ (x64, Arm) | the `-setup.exe` |

Downloads are on [Releases](../../releases). The app updates itself (the `.deb` updates through `apt`).

- **Linux** needs GTK 3, and PulseAudio, PipeWire or ALSA for sound. `install.sh` may warn about WebKitGTK; the app doesn't use it.
- **Windows** builds aren't code-signed: in SmartScreen, choose *More info → Run anyway*. No media keys yet.

## Sign in

In your browser's developer tools, copy the `Cookie` header of any `music.youtube.com` request and paste it into the app's sign-in sheet. It's kept in the macOS Keychain, or a private file in your config directory elsewhere.

If [yt-dlp](https://github.com/yt-dlp/yt-dlp) is on your `PATH`, it's used for songs the app can't play on its own.

## Build from source

Requires Go 1.27+.

```sh
go run ./cmd/desktop           # run the app
go tool mygo build             # build for this system into build/
```

## Development

```sh
go run -tags placeholder ./cmd/desktop   # offline fake catalog, no audio
YT_SOURCE_DELAY=800ms go run -tags placeholder ./cmd/desktop   # with loading states
YT_LOG=debug go run ./cmd/desktop        # why fetches, sign-ins or streams failed
prek run --all-files                     # every check; must pass
```

Tools are pinned in `.tool-versions`; `prek install` adds the checks as a pre-commit hook. Project rules and layout: [AGENTS.md](AGENTS.md). Releasing: [docs/release.md](docs/release.md). Performance: [docs/performance.md](docs/performance.md).

## License

[MIT](LICENSE)
