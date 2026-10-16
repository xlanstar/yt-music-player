#!/bin/sh
# Builds ytmd and ytm, the background player and its command line, into
# resources/<platform>/, which `mygo build` ships with that platform's app
# (beside the executable, or in Contents/Resources), signs with it and, for
# darwin/universal, merges into universal binaries (docs/release.md):
#
#   scripts/build-helpers.sh <platform>…
#
#   platform   darwin-arm64, darwin-amd64, linux-amd64, linux-arm64,
#              windows-amd64 or windows-arm64; each one's directory is
#              made anew
#
# A universal Mac app needs both darwin ones: mygo pairs their files, and
# all but code must be identical. Built as mygo builds the app: no cgo,
# trimmed paths, stripped, with mygo.json's version.
set -eu

cd "$(dirname "$0")/.."

usage() {
	sed -n '2,15s/^# \{0,1\}//p' "$0" >&2
	exit 2
}

[ $# -gt 0 ] || usage
for platform in "$@"; do
	case $platform in
	darwin-arm64 | darwin-amd64 | linux-amd64 | linux-arm64 | windows-amd64 | windows-arm64) ;;
	*) usage ;;
	esac
done

version=$(jq -r .version mygo.json)
identifier=$(jq -r .identifier mygo.json).daemon
# ytmd's bundle, which internal/ipc looks for (daemonBundle).
bundle="YT Music Player Helper"

# build <goos> <goarch> <out> <package> [ldflags]
build() {
	GOOS=$1 GOARCH=$2 CGO_ENABLED=0 go build -trimpath \
		-ldflags "-s -w -X yt-music-player/internal/version.Version=$version${5:+ $5}" \
		-o "$3" "$4"
}

# The bundle makes ytmd an app of its own to macOS: Now Playing shows its
# name and icon, and LSUIElement keeps it out of the Dock.
info_plist() {
	cat <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleExecutable</key>
	<string>ytmd</string>
	<key>CFBundleIconFile</key>
	<string>AppIcon</string>
	<key>CFBundleIdentifier</key>
	<string>$identifier</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>CFBundleName</key>
	<string>$bundle</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>$version</string>
	<key>CFBundleVersion</key>
	<string>$version</string>
	<key>LSMinimumSystemVersion</key>
	<string>12.0</string>
	<key>LSUIElement</key>
	<true/>
</dict>
</plist>
EOF
}

for platform in "$@"; do
	goos=${platform%-*}
	goarch=${platform#*-}
	out=resources/$platform
	rm -rf "$out"
	mkdir -p "$out"
	case $goos in
	darwin)
		contents="$out/$bundle.app/Contents"
		mkdir -p "$contents/MacOS" "$contents/Resources"
		build "$goos" "$goarch" "$contents/MacOS/ytmd" ./cmd/ytmd
		info_plist >"$contents/Info.plist"
		cp assets/icon/icon.icns "$contents/Resources/AppIcon.icns"
		build "$goos" "$goarch" "$out/ytm" ./cmd/ytm
		;;
	linux)
		build "$goos" "$goarch" "$out/ytmd" ./cmd/ytmd
		build "$goos" "$goarch" "$out/ytm" ./cmd/ytm
		# For the Debian package's /opt/yt-music-player/ytmd; ytm
		# autostart on writes the real one (internal/autostart).
		cp packaging/linux/ytmd.service "$out/"
		;;
	windows)
		# No console window: the daemon runs detached.
		build "$goos" "$goarch" "$out/ytmd.exe" ./cmd/ytmd -H=windowsgui
		build "$goos" "$goarch" "$out/ytm.exe" ./cmd/ytm
		;;
	esac
	echo "Built ytmd and ytm $version into $out"
done
