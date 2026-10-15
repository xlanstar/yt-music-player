#!/bin/sh
# Measures the running app's memory and frames, the numbers "Memory on
# macOS" in docs/performance.md tells, into perf/footprint-latest.txt, and
# compares them with perf/footprint-baseline.txt. It opens the window,
# which needs the focus (click it if it stays behind: macOS lets no app
# take it from the one in front), and later gives it to Finder: about 70
# seconds, hands off the keyboard and mouse after that click.
#
#   scripts/footprint.sh           the placeholder catalog, offline
#   scripts/footprint.sh -live     the signed-in account, as thumbnails cost
#   scripts/footprint.sh -update   make perf/footprint-latest.txt the baseline,
#                                  after a placeholder run of a deliberate change
set -eu

cd "$(dirname "$0")/.."

latest=perf/footprint-latest.txt
baseline=perf/footprint-baseline.txt

usage() {
	sed -n '2,13s/^# \{0,1\}//p' "$0" >&2
	exit 2
}

live=
update=
while [ $# -gt 0 ]; do
	case $1 in
	-live) live=1 ;;
	-update) update=1 ;;
	*) usage ;;
	esac
	shift
done

if [ -n "$update" ]; then
	# The account's catalog changes with the account: only the
	# placeholder's numbers compare from one machine and day to another.
	grep -q '^# source: placeholder$' "$latest" 2>/dev/null || {
		echo "footprint.sh: $latest is no placeholder run: run scripts/footprint.sh first" >&2
		exit 1
	}
	! grep -q '^# invalid:' "$latest" || {
		echo "footprint.sh: $latest has invalid phases: run it again" >&2
		exit 1
	}
	cp "$latest" "$baseline"
	echo "footprint.sh: $latest is the baseline now; commit it with the change it measures" >&2
	exit 0
fi

[ "$(uname -s)" = Darwin ] || {
	echo "footprint.sh: footprint(1) and the harness are macOS only" >&2
	exit 1
}

source=placeholder
# The placeholder build tag swaps YouTube Music for the offline fake.
tags=memprof,placeholder
[ -z "$live" ] || { source=live; tags=memprof; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

# An app bundle, opened as the Finder would: macOS gives the focus to an
# app the user launches, and keeps it from one a shell starts.
app=$tmp/YT\ Music\ Footprint.app
mkdir -p "$app/Contents/MacOS"
go build -tags "$tags" -o "$app/Contents/MacOS/yt-music-player" .
cat >"$app/Contents/Info.plist" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleExecutable</key><string>yt-music-player</string>
	<key>CFBundleIdentifier</key><string>dev.yt-music-player.footprint</string>
	<key>CFBundleName</key><string>YT Music Footprint</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
EOF
echo "footprint.sh: measuring ($source), about 70 seconds: click the window if it stays behind, then hands off" >&2
{
	echo "# yt-music-player footprint, scripts/footprint.sh"
	echo "# source: $source"
	echo "# date: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
	echo "# go: $(go version)"
	echo "# cpu: $(sysctl -n machdep.cpu.brand_string 2>/dev/null || uname -m), macOS $(sw_vers -productVersion)"
	echo "# git: $(git rev-parse --short HEAD 2>/dev/null || echo "no commit"), $(git status --porcelain 2>/dev/null | wc -l | tr -d ' ') files changed"
} >"$tmp/out.txt"

# Port 0: no clash with another memprof build's pprof (docs/performance.md).
open -n -F -W --env YT_HARNESS=footprint --env MYGO_FRAME_STATS=all \
	--env YT_PPROF_ADDR=127.0.0.1:0 \
	--stdout "$tmp/report.txt" --stderr "$tmp/log.txt" "$app" &
pid=$!
# Its own sleep, stopped with it, so that no child holds the output open.
(
	sleep 420 &
	s=$!
	trap 'kill "$s"; exit 0' TERM
	wait "$s"
	pkill -f "$app/Contents/MacOS/"
) >/dev/null 2>&1 &
watchdog=$!
status=0
wait "$pid" || status=$?
kill "$watchdog" 2>/dev/null || :
cat "$tmp/report.txt" >>"$tmp/out.txt" 2>/dev/null || :

if [ "$status" != 0 ] || ! grep -q '^# end$' "$tmp/out.txt"; then
	echo "footprint.sh: the harness did not finish (exit $status); its log ends:" >&2
	tail -n 20 "$tmp/log.txt" >&2
	exit 1
fi
mkdir -p perf
mv "$tmp/out.txt" "$latest"
cat "$latest"
# A phase the window was not right for, as the user clicking elsewhere
# or macOS keeping the focus away, measured another case.
invalid=
if grep -q '^# invalid:' "$latest"; then
	invalid=1
	echo "footprint.sh: invalid phases (see '# invalid'): run again, hands off" >&2
fi

if [ ! -f "$baseline" ]; then
	echo "footprint.sh: no $baseline to compare with: scripts/footprint.sh -update makes one" >&2
	exit "${invalid:-0}"
fi
if ! grep -q "^# source: $source\$" "$baseline"; then
	echo "footprint.sh: $baseline measured another source; not compared" >&2
	exit "${invalid:-0}"
fi

# Each phase's frames a minute and footprint, baseline then latest.
echo
awk '
	$1 !~ /^phase=/ { next }
	{
		for (i = 1; i <= NF; i++) {
			k = substr($i, 1, index($i, "=") - 1)
			f[k] = substr($i, index($i, "=") + 1)
		}
		line = f["frames/min"] SUBSEP f["MB-mean"] SUBSEP f["gfx-MB-mean"]
	}
	FNR == NR { base[f["phase"]] = line; next }
	f["phase"] in base {
		split(base[f["phase"]], b, SUBSEP)
		split(line, l, SUBSEP)
		if (!header++) printf "%-18s %15s %15s %15s\n", "phase", "frames/min", "MB-mean", "gfx-MB-mean"
		printf "%-18s %6.1f → %6.1f %6.0f → %6.0f %6.0f → %6.0f\n", f["phase"], b[1], l[1], b[2], l[2], b[3], l[3]
	}
' "$baseline" "$latest"
exit "${invalid:-0}"
