#!/bin/sh
# Builds the app: ytmd and ytm into resources/<platform>/, then
# `go tool mygo build`, which ships, signs and, for darwin/universal, merges
# them (docs/release.md#the-helpers). A bare `mygo build` ships the helpers
# a previous run left there, or none.
#
#   scripts/build.sh [-platform <os/arch>,…] [mygo build flags…]
#
#   -platform   as mygo build's: darwin/universal, darwin/arm64,
#               linux/amd64, windows/arm64…; this machine's by default
set -eu

cd "$(dirname "$0")/.."

usage() {
	sed -n '2,11s/^# \{0,1\}//p' "$0" >&2
	exit 2
}

platforms=
prev=
for arg; do
	case $prev,$arg in
	-platform,* | --platform,*) platforms=$arg ;;
	*,-platform=* | *,--platform=*) platforms=${arg#*=} ;;
	*,-h | *,-help | *,--help) usage ;;
	esac
	prev=$arg
done

version=$(jq -r .version mygo.json)
identifier=$(jq -r .identifier mygo.json).daemon
# ytmd's bundle, which internal/ipc looks for (daemonBundle).
bundle="YT Music Player Helper"

# build <goos> <goarch> <out> <package> [ldflags]: as mygo builds the app.
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

# helpers <goos> <goarch> builds resources/<goos>-<goarch>/ anew.
helpers() {
	out=resources/$1-$2
	rm -rf "$out"
	mkdir -p "$out"
	case $1 in
	darwin)
		contents="$out/$bundle.app/Contents"
		mkdir -p "$contents/MacOS" "$contents/Resources"
		build "$1" "$2" "$contents/MacOS/ytmd" ./cmd/ytmd
		info_plist >"$contents/Info.plist"
		cp assets/icon/icon.icns "$contents/Resources/AppIcon.icns"
		build "$1" "$2" "$out/ytm" ./cmd/ytm
		;;
	linux)
		build "$1" "$2" "$out/ytmd" ./cmd/ytmd
		build "$1" "$2" "$out/ytm" ./cmd/ytm
		# For the Debian package's /opt/yt-music-player/ytmd; ytm
		# autostart on writes the real one (internal/autostart).
		cp packaging/linux/ytmd.service "$out/"
		;;
	windows)
		# No console window: the daemon runs detached.
		build "$1" "$2" "$out/ytmd.exe" ./cmd/ytmd -H=windowsgui
		build "$1" "$2" "$out/ytm.exe" ./cmd/ytm
		;;
	*)
		echo "build.sh: no helpers for $1/$2" >&2
		exit 2
		;;
	esac
}

if [ -z "$platforms" ]; then
	platforms=$(go env GOOS)/$(go env GOARCH)
	set -- -platform "$platforms" "$@"
fi

for p in $(echo "$platforms" | tr , ' '); do
	case $p in
	darwin/universal)
		helpers darwin arm64
		helpers darwin amd64
		;;
	*/*) helpers "${p%/*}" "${p#*/}" ;;
	*) usage ;;
	esac
done

exec go tool mygo build "$@"
