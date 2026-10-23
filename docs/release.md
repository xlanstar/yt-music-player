# Releasing

One command to release, one workflow on GitHub, one approval to publish to GitHub and Homebrew. Releases are for macOS (universal), Linux and Windows (amd64 and arm64 each) and ship ytmd and ytm beside the app. The macOS app is signed with a Developer ID and notarized; no certificate signs the Windows app, so SmartScreen warns at first run.

- **The version** is `version` in `mygo.json`, the only place it is written: the app reads it, `mygo build` puts it in `Info.plist`, the disk image's name and the update manifest, `scripts/build.sh` in ytmd and ytm. `0.0.0` means never released.
- **A release** is an annotated tag `vX.Y.Z` on `main` or [a branch `X.Y`](#older-versions), made only by `scripts/release.sh`. Tags are never moved, deleted or reused.
- **Semantic Versioning**, before 1.0: `minor` for features and changes of behavior, `patch` for fixes only. From `0.0.0`, `minor` makes `v0.1.0`.
- **CHANGELOG.md**: each change users notice goes under `## [Unreleased]`, in the same change as the code. That section becomes the release's notes, on GitHub and in the app's update window.

## Making a release

```sh
scripts/release.sh minor        # or patch, major
```

From a clean `main` equal to `origin/main`, a non-empty Unreleased section and `updates` in `mygo.json`, it shows the next version and its notes and asks first. Then it runs `prek run --all-files`, moves Unreleased under `## [X.Y.Z] - <date>`, sets the version in `mygo.json`, commits `chore(release): vX.Y.Z`, tags, and pushes `main` and the tag at once (`--atomic`).

The tag starts `.github/workflows/release.yml` (`gh run watch` follows it):

1. **tag**: `scripts/release.sh check` refuses a tag that is not `vX.Y.Z`, differs from `mygo.json`, has no CHANGELOG section or is on neither `main` nor `X.Y`, and a `mygo.json` without `updates`.
2. **checks**: the CI workflow, prek on macOS, as on every push and pull request.
3. **macos**, in the `release` environment: `scripts/build.sh -platform darwin/universal -sign <identity>` builds the helpers for both architectures into one app, signs it with the hardened runtime, notarizes and staples it and the disk image, and signs the update with `MYGO_UPDATER_PRIVATE_KEY`. It checks the signatures, the tickets and that ytmd and ytm are universal, and keeps `YT-Music-Player-X.Y.Z-macos-universal.dmg` and the update files.
4. **linux-windows**, a job per system on Ubuntu, in the `release` environment: `scripts/build.sh -platform linux/amd64,linux/arm64` (or `windows/…`) cross-compiles everything, MyGo needing no cgo, and signs the updates; NSIS makes the Windows installers. It checks each executable's architecture, the consoles (none for `ytmd.exe`) and the Debian package's `/opt/yt-music-player/ytmd` and `/usr/bin/ytm`, and keeps the `.deb`s, `-setup.exe`s, Linux's `install.sh` and the update files.
5. **draft**: gathers the build jobs' files, refusing a name two of them made, writes `SHA256SUMS` of them all, attests their provenance (public repositories), then creates the **draft** release with the files and the notes.
6. **publish**, in the `publish` environment, waits for its reviewer's approval, then runs `scripts/release.sh publish X.Y.Z` (below) on macOS, for `brew style`.

Before approving, check the draft: download its files, `shasum -a 256 -c SHA256SUMS`, and on each system:

- **macOS:** the app opens without Gatekeeper asking (`spctl -a -vv` says `source=Notarized Developer ID`); sign in, play a song, close the window: the song plays on, Now Playing shows *YT Music Player Helper*, and `"/Applications/YT Music Player.app/Contents/Resources/ytm" status` shows it.
- **Linux:** `sh install.sh` the archive next to it, or `sudo apt install ./YT-Music-Player-X.Y.Z-linux-amd64.deb`, then `ytm version` and `ytm status` from the PATH (`~/.local/bin/ytm` or `/usr/bin/ytm`).
- **Windows:** run the setup through SmartScreen's *More info → Run anyway*, then `"%LOCALAPPDATA%\Programs\YT Music Player\ytm.exe" status`.

Then approve: the run's page → *Review deployments* → `publish` → *Approve and deploy*. *Reject*, or no answer within 30 days, publishes nothing.

`scripts/release.sh publish X.Y.Z` checks that the draft has every platform's downloads and `update-<platform>.json`, that `SHA256SUMS` lists every file and matches it, and each manifest against the version and the files it links to; publishes the release as the latest; then runs `tap`. **Publishing ships the update**: installed apps read the manifest of the latest release, never a draft's.

## Older versions

`minor` and `major` also push a branch for the version they leave, `X.Y` at its last release: releasing `0.3.0` after `0.2.1` makes `0.2` at `v0.2.1`.

To patch it, cherry-pick fixes into `X.Y` (or open a pull request against it), note them under its Unreleased, then run `scripts/release.sh patch` on `X.Y`. The release is published but not marked latest, so installed apps and Homebrew stay on the newer version.

## The helpers

`ytmd` and `ytm` ship as MyGo [platform resources](https://mygo.egoist.dev/docs/distribution.md#platform-resources): `scripts/build.sh` builds them into `resources/<platform>/` (ignored by git) as `mygo build` builds the app (no cgo, `-trimpath`, stripped, `mygo.json`'s version), then runs `mygo build`, which copies each target's directory into its app, signs it and merges `darwin/universal`'s two. A bare `mygo build` ships whatever an earlier run left there, or nothing.

- **macOS**: `Contents/Resources/YT Music Player Helper.app` (ytmd in a bundle of its own, `dev.lanstar.ytmusicplayer.daemon`, `LSUIElement`, the app's icon) and `Contents/Resources/ytm`. Now Playing shows the bundle's name, which is not the app's: LaunchServices registers nested apps too and would find two *YT Music Player*s.
- **Linux**: `ytmd`, `ytm` and `ytmd.service` (`packaging/linux/`, for the Debian package's `/opt/yt-music-player`; `ytm autostart on` writes the real one) beside the app. `mygo.json`'s `linux.command` links `ytm` on the PATH to the app, which runs the `ytm` beside it when started under that name.
- **Windows**: `ytmd.exe` (`-H=windowsgui`, no console) and `ytm.exe` beside the app. The installer can neither stop ytmd nor remove the login entry or the `Path` entry: before uninstalling, choose *Quit and Stop Playback* and turn both settings off.

## Security

Only the workflow's `draft` and `publish` jobs can write to the repository; the others read only, checkouts keep no credentials, and actions are pinned by commit, which Dependabot (`.github/dependabot.yml`) bumps weekly with the Go modules. The signing secrets are in the `release` environment, which only `v*` tags can use, and only the steps that sign read them.

`TAP_TOKEN`, which writes only the tap, is in the `publish` environment, whose job waits for approval.

## When something fails

| Where | What to do |
|---|---|
| `release.sh`, before the push | Nothing is pushed. A failed commit restores `mygo.json` and `CHANGELOG.md`; a failed push keeps the commit and tag local, push them again with the command it prints. |
| The workflow, something flaky | Re-run the failed jobs. A rerun replaces the files of its draft, never of a published release. |
| The workflow, signing | A secret is missing, expired or revoked: replace it ([signing](#signing-and-notarization), [updates](#updates)) and re-run the failed jobs. |
| The workflow, notarization | `Invalid`: the job's log has the submission ID, and `xcrun notarytool log <id> --keychain-profile notary` on your Mac says why. Fix on `main` and release the next patch. |
| The workflow, a real bug | Reject the publish job, delete the draft if there is one, fix on `main`, and release the next patch. The failed tag stays, unreleased. |
| `publish`, its checks | Nothing is published: a missing or wrong file is a real bug, as above. Without `TAP_TOKEN`, add it and re-run the job. |
| `publish`, the tap | The release is published; fix, then re-run the job, which then only updates the tap, or run `scripts/release.sh tap X.Y.Z`. |
| `publish`, GitHub down | Re-run the job, or run `scripts/release.sh publish X.Y.Z` with a `gh` login that can write the repository. |
| A published release | Never replace its files. Fix on `main` and release the next patch, which installed apps update to; edit the old release's notes to point to it. |

## Once, on GitHub

- Push the repository with `main` as its default branch, **public**: apps read their updates from its latest release without credentials.
- Settings → Rules: protect `main` and branches `[0-9]*.[0-9]*` (require the CI check, no force pushes) and tags `v*` (no updates or deletions).
- Settings → General → Releases: turn on release immutability, so that a published release's files and tag cannot change.
- Settings → Environments: `release`, its deployment branches and tags limited to the tag rule `v*`, with the secrets of [signing](#signing-and-notarization) and [updates](#updates).
- Settings → Environments: `publish`, limited to `v*` likewise, *Required reviewers* yourself, and the secret `TAP_TOKEN`: a fine-grained token with *Contents: Read and write* on `xlanstar/homebrew-tap` only. Renew it before it expires.
- Anyone can check a download: `gh attestation verify YT-Music-Player-X.Y.Z-macos-universal.dmg --repo xlanstar/yt-music-player`.

## Signing and notarization

Releases are signed with `Developer ID Application: PENG HUAN CHENG (LJJN3L2PS6)` and notarized, so Gatekeeper opens them. Only the workflow signs: `mygo.json` has no signing settings, so `scripts/build.sh` signs ad hoc on any Mac and needs no credentials; the macos job passes `-sign` and adds `macos.notarize`, with its keychain, to its checkout's `mygo.json`.

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

The app updates itself ([MyGo's updates](https://mygo.egoist.dev/docs/updates.md)) from `mygo.json`'s `updates`: it reads `update-<platform>.json` of `updates.github`'s latest release, checks the archive's or delta's Ed25519 signature against `updates.publicKey`, and replaces itself, but not the Debian package, which `apt` updates, nor on Windows an app the setup did not install. The relaunched app hands off to its own ytmd ([docs/daemon.md](daemon.md#versions-and-hand-off)).

The build jobs sign the updates with the secret key:

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
- `tap` clones and pushes the tap with `TAP_TOKEN` in the workflow, else with your `gh` login; `brew style` checks the cask first when Homebrew is installed.
- The app updates itself, also when brew installed it: the cask says `auto_updates true`, and brew, which compares the installed app's version, upgrades only an app that did not update (Homebrew 6 and later; older versions with `--greedy`).
- `scripts/install.sh [-universal]` installs this checkout through the same cask, from a local tap (`local/yt-music-player`) and the local disk image, its quarantine removed, as it is signed ad hoc. The app still updates itself to a newer release.
- The cask links `ytm` into Homebrew's `bin`, and `brew uninstall` (upgrades too) first stops the background player with it. The LaunchAgent stays, for upgrades; without the app it removes itself at the next login. `--zap` removes it at once and forgets the sign-in (the Keychain item `yt-music-player`), settings, themes, the player's state and caches.
- After a release: `brew update && brew outdated --cask` lists nothing for an app that updated itself, and `brew audit --cask --online xlanstar/tap/yt-music-player`.
