# Releasing

One command to release, one workflow on GitHub, one command to publish to GitHub and Homebrew. Releases are for macOS (one app for Apple silicon and Intel), Linux and Windows (amd64 and arm64 each), and update the installed apps. Each ships the background player `ytmd` and the command line `ytm` beside the app. The macOS app is signed with a Developer ID and notarized; no certificate signs the Windows app, so SmartScreen warns before it first runs.

- **The version** is `version` in `mygo.json`, the only place it is written: the app reads it (`App.Version()`), `mygo build` puts it in `Info.plist`, the disk image's name and the update manifest. `0.0.0` means never released.
- **A release** is an annotated tag `vX.Y.Z` on `main`, made only by `scripts/release.sh`. Tags are never moved, deleted or reused.
- **Semantic Versioning**, before 1.0: `minor` for features and changes of behavior, `patch` for fixes only. From `0.0.0`, `minor` makes `v0.1.0`.
- **CHANGELOG.md**: each change users notice goes under `## [Unreleased]`, in the same change as the code. That section becomes the release's notes, on GitHub and in the app's update window.

## Making a release

```sh
scripts/release.sh minor        # or patch, major
```

From a clean `main` equal to `origin/main` with a non-empty Unreleased section and `updates` in `mygo.json`, it shows the next version and its notes and asks before doing anything. Then it runs `prek run --all-files`, moves Unreleased under `## [X.Y.Z] - <date>`, sets the version in `mygo.json`, commits `chore(release): vX.Y.Z`, tags, and pushes `main` and the tag together (`--atomic`: both or neither).

The tag starts `.github/workflows/release.yml` (`gh run watch` follows it):

1. **tag**: `scripts/release.sh check` refuses a tag that is not `vX.Y.Z`, differs from `mygo.json`, has no CHANGELOG section or is not on `main`, and a `mygo.json` without `updates`.
2. **checks**: the CI workflow, prek on macOS, as on every push and pull request.
3. **macos**, in the `release` environment: imports the Developer ID certificate and the notary credentials into a keychain of its own, then `scripts/build.sh -platform darwin/universal -sign <identity>` builds the helpers (below) and makes one app for Apple silicon and Intel, signs it with the hardened runtime, notarizes the disk image, staples the tickets and signs the update with `MYGO_UPDATER_PRIVATE_KEY`; `codesign`, `spctl` and `stapler` check the app and the disk image; `file` and `lipo` check that `ytmd` and `ytm` are universal, `codesign` that they are signed, `Info.plist` that the helper has the version, and the update's archive that it holds them. It keeps the disk image, the update files under the names their manifest links to (`yt-music-player-X.Y.Z-darwin-universal.tar.gz`, `update-darwin-universal.json`, and `…-to-X.Y.Z-darwin-universal.delta` from the last published versions, none for the first).
4. **linux-windows**, at the same time, in the `release` environment, a job for each system on Ubuntu: `scripts/build.sh -platform linux/amd64,linux/arm64` (or `windows/…`) cross-compiles, MyGo needing no cgo, and signs the updates with the same key; `apt` installs NSIS for the Windows installers. `file` checks each executable's architecture, the app's and the helpers' (`ytmd.exe` without a console, `ytm.exe` with one), and the update's archive and the Debian package must hold the helpers (the package also `ytmd.service` and its `/usr/bin/ytm`). It keeps `YT-Music-Player-X.Y.Z-linux-<arch>.deb`, `YT-Music-Player-X.Y.Z-windows-<arch>-setup.exe`, `install.sh` (Linux's, the same for both architectures), and each platform's update files as above (`yt-music-player-X.Y.Z-<platform>.tar.gz`, which `install.sh` installs on Linux, `update-<platform>.json`, deltas).
5. **publish**: gathers the build jobs' files, refusing a name two of them made, writes `SHA256SUMS` of them all, attests their provenance (public repositories), then creates the **draft** release with the files and the notes.

Then check the draft: download its files, `shasum -a 256 -c SHA256SUMS`, open the app, which opens without Gatekeeper asking (`spctl -a -vv` says `source=Notarized Developer ID`), sign in, play a song, close the window: the song plays on, Now Playing shows *YT Music Player Helper*, and `"/Applications/YT Music Player.app/Contents/Resources/ytm" status` shows it; on Linux, `sh install.sh` the archive next to it, or `sudo apt install ./YT-Music-Player-X.Y.Z-linux-amd64.deb`, then `ytm version` and `ytm status` from the PATH (`~/.local/bin/ytm` or `/usr/bin/ytm`); on Windows, run the setup through SmartScreen's *More info → Run anyway*, then `"%LOCALAPPDATA%\Programs\YT Music Player\ytm.exe" status`. Publish it:

```sh
scripts/release.sh publish X.Y.Z
```

It checks that the draft has every platform's downloads and `update-<platform>.json` (`darwin-universal`, `linux-amd64`, `linux-arm64`, `windows-amd64`, `windows-arm64`), that `SHA256SUMS` lists every file and matches it, and each manifest against the version and the files it links to, publishes the release as the latest, then runs `tap`. **Publishing ships the update**: installed apps read the manifest of the latest release, never a draft's.

## The helpers

`ytmd` and `ytm` are separate programs (`cmd/ytmd`, `cmd/ytm`) that the app ships as MyGo [platform resources](https://mygo.egoist.dev/docs/distribution.md#platform-resources): `scripts/build.sh` builds them into `resources/<platform>/` (ignored by git) for each target, as `mygo build` builds the app (no cgo, `-trimpath`, stripped) with `mygo.json`'s version in `internal/version`, then runs `mygo build`, which copies the directory of each target into its app, signs what it holds with the app and, for `darwin/universal`, merges `darwin-arm64` with `darwin-amd64` into universal binaries. A bare `mygo build` ships whatever an earlier run left there, or nothing.

- **macOS**: `Contents/Resources/YT Music Player Helper.app` (`ytmd` in a bundle of its own, `dev.lanstar.ytmusicplayer.daemon`, `LSUIElement`, the app's icon, whose folder name Now Playing shows; not the app's name, which LaunchServices, registering nested apps too, would then find twice) and `Contents/Resources/ytm`. Both architectures' `Info.plist` and icon are the same file, as a universal build requires.
- **Linux**: `ytmd`, `ytm` and `ytmd.service` (`packaging/linux/ytmd.service`, for the Debian package's path; `ytm autostart on` writes the real one) beside the app. `mygo.json`'s `linux.command` links `ytm` on the PATH to the app, which runs the `ytm` beside it when started under that name (`cmd/desktop/ytm_linux.go`).
- **Windows**: `ytmd.exe` (`-H=windowsgui`, no console) and `ytm.exe` beside the app.

Only the workflow's `publish` job can write to the repository; every job has read access only otherwise, credentials are not kept in checkouts, and the actions are pinned by commit; Dependabot (`.github/dependabot.yml`) bumps them and the Go modules weekly. The signing secrets are in the `release` environment, which only `v*` tags can use, and only the build jobs' steps that sign read them.

## When something fails

| Where | What to do |
|---|---|
| `release.sh`, before the push | Nothing is pushed. A failed commit restores `mygo.json` and `CHANGELOG.md`; a failed push keeps the commit and tag local, push them again with the command it prints. |
| The workflow, something flaky | Re-run the failed jobs. A rerun replaces the files of its draft, never of a published release. |
| The workflow, signing | A secret is missing, expired or revoked: replace it ([signing](#signing-and-notarization), [updates](#updates)) and re-run the failed jobs. |
| The workflow, notarization | `Invalid`: the job's log has the submission ID, and `xcrun notarytool log <id> --keychain-profile notary` on your Mac says why. Fix on `main` and release the next patch. |
| The workflow, a real bug | Delete the draft if there is one, fix on `main`, and release the next patch. The failed tag stays, unreleased. |
| `publish`, the tap | The release is published; fix, then `scripts/release.sh tap X.Y.Z`. |
| A published release | Never replace its files. Fix on `main` and release the next patch, which installed apps update to; edit the old release's notes to point to it. |

## Once, on GitHub

- Push the repository with `main` as its default branch, **public**: apps read their updates from its latest release without credentials.
- Settings → Rules: protect `main` (require the CI check, no force pushes) and tags `v*` (no updates or deletions).
- Settings → General → Releases: turn on release immutability, so that a published release's files and tag cannot change.
- Settings → Environments: `release`, its deployment branches and tags limited to the tag rule `v*`, with the secrets of [signing](#signing-and-notarization) and [updates](#updates).
- Anyone can check a download: `gh attestation verify YT-Music-Player-X.Y.Z-macos-universal.dmg --repo xlanstar/yt-music-player`.

## Signing and notarization

Releases are signed with `Developer ID Application: PENG HUAN CHENG (LJJN3L2PS6)` and notarized, so Gatekeeper opens them. Only the workflow signs: `mygo.json` has no signing settings, so `go tool mygo build` signs ad hoc on any Mac and needs no credentials; the macos job passes `-sign` and adds `macos.notarize`, with its keychain, to its checkout's `mygo.json`.

The `release` environment's secrets:

```sh
# Keychain Access → My Certificates → Developer ID Application: … → Export, a .p12 with a password
base64 -i developer-id.p12 | gh secret set MACOS_CERTIFICATE --env release
rm developer-id.p12
gh secret set MACOS_CERTIFICATE_PASSWORD --env release
gh secret set APPLE_ID --env release             # the developer account's Apple ID
gh secret set APPLE_APP_PASSWORD --env release   # account.apple.com → Sign-In and Security → App-Specific Passwords
```

On your Mac, once, for `notarytool log` and to try a signed build (the password is the app-specific one):

```sh
xcrun notarytool store-credentials notary --apple-id <Apple ID> --team-id LJJN3L2PS6
scripts/build.sh -platform darwin/universal -sign "Developer ID Application: PENG HUAN CHENG (LJJN3L2PS6)"
xcrun notarytool submit "build/darwin-universal/YT Music Player X.Y.Z.dmg" --keychain-profile notary --wait
```

The certificate expires after five years, and a revoked app-specific password fails notarization: replace its secret. A new certificate of the same team keeps the app's identity, so the Keychain still trusts it.

## Updates

The app updates itself ([MyGo's updates](https://mygo.egoist.dev/docs/updates.md)): `mygo.json`'s `updates` holds the public key and the repository, and the app reads its platform's `update-<platform>.json` of the latest release, checks the archive's or delta's Ed25519 signature against the key built into it, and replaces itself: on Windows when the setup installed it, on Linux when `install.sh` did (in `~/.local`), never from the Debian package, which `apt` updates. The build jobs sign the updates with the secret key:

```sh
go tool mygo keygen   # prints where it wrote mygo-update.key and mygo-update.pub
gh secret set MYGO_UPDATER_PRIVATE_KEY --env release < <dir>/mygo-update.key
```

- Keep `mygo-update.key` in a password manager too, never in the repository: installed apps accept only updates signed with it, so losing it strands them, and whoever has it can ship code to every user.
- Never change `updates.publicKey` alone: apps of the old key would refuse the next update.
- Without the secret key `mygo build` warns and writes no update; the build jobs then fail rather than release a version nothing updates to.
- Builds on your Mac, without the key, write no update files but Linux's archive.

## Homebrew

`brew install --cask xlanstar/tap/yt-music-player` installs from [xlanstar/homebrew-tap](https://github.com/xlanstar/homebrew-tap).

- `packaging/homebrew/yt-music-player.rb` is the cask's template. Its `Casks/yt-music-player.rb` in the tap is only written by `scripts/release.sh tap X.Y.Z`; change the template, never the tap.
- `tap` uses the disk image of the published release, as users download it, never a draft's; it refuses an older version than the tap's and commits nothing when nothing changed, so it can always run again, after a fix to the template too.
- `tap` clones and pushes the tap with your `gh` login; `brew style` checks the cask first when Homebrew is installed.
- The app updates itself, also when brew installed it: the cask says `auto_updates true`, and brew, which compares the installed app's version, upgrades only an app that did not update (Homebrew 6 and later; older versions with `--greedy`).
- The cask links `ytm` into Homebrew's `bin`. `brew uninstall` first stops the background player with it (`ytm daemon stop`), on upgrades too, as Homebrew runs a cask's uninstall steps then.
- `brew uninstall --zap` also removes the login item of the background player (its LaunchAgent), which a plain uninstall leaves so that upgrades keep it (without the app, it removes itself at the next login), forgets the sign-in (the Keychain item `yt-music-player`), settings, themes and the artwork cache.
- After a release: `brew update && brew outdated --cask` lists nothing for an app that updated itself, and `brew audit --cask --online xlanstar/tap/yt-music-player`.
