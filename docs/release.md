# Releasing

One command to release, one workflow on GitHub, one command to publish to GitHub and Homebrew. Releases are signed with a Developer ID, notarized, and update the installed apps.

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
2. **checks**: the CI workflow, prek on Linux, as on every push and pull request.
3. **build**, in the `release` environment: imports the Developer ID certificate and the notary credentials into a keychain of its own, then `go tool mygo build -platform darwin/universal -sign <identity>` makes one app for Apple silicon and Intel, signs it with the hardened runtime, notarizes the disk image, staples the tickets and signs the update with `MYGO_UPDATER_PRIVATE_KEY`; `codesign`, `spctl` and `stapler` check the app and the disk image. It keeps the disk image, the update files under the names their manifest links to (`yt-music-player-X.Y.Z-darwin-universal.tar.gz`, `update-darwin-universal.json`, and `…-to-X.Y.Z-darwin-universal.delta` from the last published versions, none for the first), and `SHA256SUMS` of them all.
4. **publish**: attests the files' provenance (public repositories), then creates the **draft** release with the files and the notes.

Then check the draft: download its files, `shasum -a 256 -c SHA256SUMS`, open the app, which opens without Gatekeeper asking (`spctl -a -vv` says `source=Notarized Developer ID`), sign in, play a song. Publish it:

```sh
scripts/release.sh publish X.Y.Z
```

It checks the draft's files against `SHA256SUMS`, and its `update-darwin-universal.json` against the version and the files it links to, publishes the release as the latest, then runs `tap`. **Publishing ships the update**: installed apps read the manifest of the latest release, never a draft's.

Only the workflow's `publish` job can write to the repository; every job has read access only otherwise, credentials are not kept in checkouts, and the actions are pinned by commit; Dependabot (`.github/dependabot.yml`) bumps them and the Go modules weekly. The signing secrets are in the `release` environment, which only `v*` tags can use, and only the build job's steps that sign read them.

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

Releases are signed with `Developer ID Application: PENG HUAN CHENG (LJJN3L2PS6)` and notarized, so Gatekeeper opens them. Only the workflow signs: `mygo.json` has no signing settings, so `go tool mygo build` signs ad hoc on any Mac and needs no credentials; the build job passes `-sign` and adds `macos.notarize`, with its keychain, to its checkout's `mygo.json`.

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
go tool mygo build -platform darwin/universal -sign "Developer ID Application: PENG HUAN CHENG (LJJN3L2PS6)"
xcrun notarytool submit "build/darwin-universal/YT Music Player X.Y.Z.dmg" --keychain-profile notary --wait
```

The certificate expires after five years, and a revoked app-specific password fails notarization: replace its secret. A new certificate of the same team keeps the app's identity, so the Keychain still trusts it.

## Updates

The app updates itself ([MyGo's updates](https://mygo.egoist.dev/docs/updates.md)): `mygo.json`'s `updates` holds the public key and the repository, and the app reads `update-darwin-universal.json` of the latest release, checks the archive's or delta's Ed25519 signature against the key built into it, and replaces itself. The build job signs the update with the secret key:

```sh
go tool mygo keygen   # prints where it wrote mygo-update.key and mygo-update.pub
gh secret set MYGO_UPDATER_PRIVATE_KEY --env release < <dir>/mygo-update.key
```

- Keep `mygo-update.key` in a password manager too, never in the repository: installed apps accept only updates signed with it, so losing it strands them, and whoever has it can ship code to every user.
- Never change `updates.publicKey` alone: apps of the old key would refuse the next update.
- Without the secret key `mygo build` warns and writes no update; the build job then fails rather than release a version nothing updates to.
- Builds on your Mac, without the key, write no update files.

## Homebrew

`brew install --cask xlanstar/tap/yt-music-player` installs from [xlanstar/homebrew-tap](https://github.com/xlanstar/homebrew-tap).

- `packaging/homebrew/yt-music-player.rb` is the cask's template. Its `Casks/yt-music-player.rb` in the tap is only written by `scripts/release.sh tap X.Y.Z`; change the template, never the tap.
- `tap` uses the disk image of the published release, as users download it, never a draft's; it refuses an older version than the tap's and commits nothing when nothing changed, so it can always run again, after a fix to the template too.
- `tap` clones and pushes the tap with your `gh` login; `brew style` checks the cask first when Homebrew is installed.
- The app updates itself, also when brew installed it: the cask says `auto_updates true`, and brew, which compares the installed app's version, upgrades only an app that did not update (Homebrew 6 and later; older versions with `--greedy`).
- `brew uninstall --zap` also forgets the sign-in (the Keychain item `yt-music-player`), settings, themes and the artwork cache.
- After a release: `brew update && brew outdated --cask` lists nothing for an app that updated itself, and `brew audit --cask --online xlanstar/tap/yt-music-player`.
