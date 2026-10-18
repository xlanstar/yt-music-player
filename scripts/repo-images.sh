#!/bin/sh
# Renders the repository's images, 2560×1280 JPEGs, from the pages in
# assets/repo-images/ into build/repo-images/:
#
#   social-preview.jpg      the link card: upload it in the repository's
#                           Settings → General → Social preview; not committed
#   readme-hero-light.jpg   README's top image, GitHub's light mode
#   readme-hero-dark.jpg    README's top image, GitHub's dark mode
#
#   scripts/repo-images.sh [-readme] [-terminal terminal.png] [window.png]
#
#   -readme      copy readme-hero-*.jpg to docs/images/, which README shows
#   window.png   the window to show; without it, the placeholder build is
#                opened with a fresh home (default theme and window size),
#                over a placeholder ytmd of its own 84 s into a song, and its
#                window captured. A shot of your own: ⌘⇧4, Space,
#                Option-click the window (no macOS shadow: the pages add one).
#   -terminal terminal.png
#                the terminal to show beside it; without it, the placeholder
#                ytm is opened in kitty (assets/repo-images/kitty.conf), over
#                the same ytmd, and its window captured.
#
# macOS only (screencapture, sips), with Google Chrome, and kitty unless
# -terminal. Capture on a Retina display: the pages draw at twice their size.
set -eu

cd "$(dirname "$0")/.."

usage() {
	sed -n '2,24s/^# \{0,1\}//p' "$0" >&2
	exit 2
}

readme=
window=
terminal=
while [ $# -gt 0 ]; do
	case $1 in
	-readme) readme=1 ;;
	-terminal)
		[ $# -gt 1 ] || usage
		terminal=$2
		shift
		;;
	-*) usage ;;
	*) [ -z "$window" ] || usage; window=$1 ;;
	esac
	shift
done

chrome="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
[ -x "$chrome" ] || {
	echo "repo-images.sh: needs Google Chrome in /Applications" >&2
	exit 1
}
for f in "$window" "$terminal"; do
	[ -z "$f" ] || [ -f "$f" ] || {
		echo "repo-images.sh: no such file: $f" >&2
		exit 1
	}
done
kitty=/Applications/kitty.app/Contents/MacOS/kitty
[ -n "$terminal" ] || [ -x "$kitty" ] || {
	echo "repo-images.sh: needs kitty in /Applications, or -terminal" >&2
	exit 1
}

out=build/repo-images
# In /tmp, short: the placeholder ytmd's socket, under a home in it, must
# fit in 103 bytes.
tmp=$(mktemp -d /tmp/repo-images.XXXXXX)
pid=
cleanup() {
	if [ -n "$pid" ]; then
		kill "$pid" 2>/dev/null || true
		wait "$pid" 2>/dev/null || true
	fi
	# The app leaves ytmd playing: stop the one it started.
	pkill -f "^$tmp/ytmd\$" || true
	rm -rf "$tmp"
}
trap cleanup EXIT
trap 'exit 1' INT TERM

# isolated runs a command in the capture's own home, with its ytmd.
isolated() {
	env HOME="$tmp/home" TMPDIR="$tmp" YTMD_PATH="$tmp/ytmd" "$@"
}

# launch runs a command as isolated does, in the background, its pid in $!:
# backgrounding isolated would give a subshell's pid, which owns no window.
launch() {
	env HOME="$tmp/home" TMPDIR="$tmp" YTMD_PATH="$tmp/ytmd" "$@" >/dev/null 2>&1 &
}

# windowOf prints the ID of pid's normal window, if it has one on screen,
# and brings pid to the front: the windows show focused.
windowOf() {
	osascript -l JavaScript -e "ObjC.import('AppKit'); ObjC.import('CoreGraphics');
		const ws = ObjC.deepUnwrap(ObjC.castRefToObject(
			\$.CGWindowListCopyWindowInfo(\$.kCGWindowListOptionOnScreenOnly, 0)));
		const w = ws.find(w => w.kCGWindowOwnerPID === $1 && w.kCGWindowLayer === 0);
		if (w) \$.NSRunningApplication.runningApplicationWithProcessIdentifier($1)
			.activateWithOptions(\$.NSApplicationActivateIgnoringOtherApps);
		w ? String(w.kCGWindowNumber) : ''"
}

# capture pid file name: saves pid's window to file, part-way into the
# song, then quits pid; name is what errors call it.
capture() {
	id=
	for _ in $(seq 60); do
		id=$(windowOf "$1")
		[ -z "$id" ] || break
		sleep 0.5
	done
	[ -n "$id" ] || {
		echo "repo-images.sh: $3 showed no window" >&2
		exit 1
	}
	# Part-way into the song, so the window shows progress.
	isolated "$tmp/ytm" seek 84 >/dev/null
	# Let the first frames and the covers draw.
	sleep 2
	# -o: no shadow, the pages add their own. -a: without attached windows,
	# such as the Caps Lock badge macOS shows by a terminal's cursor.
	screencapture -o -a -x -l"$id" "$2"
	kill "$1" 2>/dev/null || true
	wait "$1" 2>/dev/null || true
	pid=
}

if [ -z "$window" ] || [ -z "$terminal" ]; then
	# The captures play through ytmd, which the app or ytm starts: a
	# placeholder one, found through YTMD_PATH, and ytm to seek.
	go build -tags placeholder -o "$tmp/ytmd" ./cmd/ytmd
	go build -tags placeholder -o "$tmp/ytm" ./cmd/ytm
	# A home of its own: the user's theme, window size, caches and ytmd
	# stay out.
	mkdir "$tmp/home"
fi
if [ -z "$window" ]; then
	go build -tags placeholder -o "$tmp/yt-music-player" ./cmd/desktop
	launch "$tmp/yt-music-player"
	pid=$!
	window=$tmp/app-window.png
	capture "$pid" "$window" "the placeholder build"
fi
if [ -z "$terminal" ]; then
	launch "$kitty" --config assets/repo-images/kitty.conf --title ytm "$tmp/ytm"
	pid=$!
	terminal=$tmp/app-terminal.png
	capture "$pid" "$terminal" "kitty"
fi

mkdir -p "$out"
cp assets/repo-images/* assets/icon/icon.svg "$tmp/"
[ "$window" = "$tmp/app-window.png" ] || cp "$window" "$tmp/app-window.png"
[ "$terminal" = "$tmp/app-terminal.png" ] || cp "$terminal" "$tmp/app-terminal.png"

# render renders page (with its query) at 1280×640 and twice the scale, as
# a JPEG: GitHub takes images up to 1 MB.
render() {
	"$chrome" --headless=new --disable-gpu --hide-scrollbars \
		--force-device-scale-factor=2 --window-size=1280,640 \
		--screenshot="$tmp/$2.png" "file://$tmp/$1" 2>/dev/null
	sips -s format jpeg -s formatOptions 88 "$tmp/$2.png" --out "$out/$2.jpg" >/dev/null
	echo "$out/$2.jpg"
}

render social-preview.html social-preview
render readme-hero.html readme-hero-light
render 'readme-hero.html?theme=dark' readme-hero-dark

if [ -n "$readme" ]; then
	mkdir -p docs/images
	cp "$out/readme-hero-light.jpg" "$out/readme-hero-dark.jpg" docs/images/
	echo "docs/images/readme-hero-{light,dark}.jpg updated"
fi
