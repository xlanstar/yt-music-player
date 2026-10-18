#!/bin/sh
# Measures the running app's memory and frames, the numbers "Memory on
# macOS" in docs/performance.md tells, into perf/footprint-latest.txt, and
# compares them with perf/footprint-baseline.txt. It opens the window,
# which needs the focus (click it if it stays behind: macOS lets no app
# take it from the one in front), and later gives it to Finder: about 70
# seconds, hands off the keyboard and mouse after that click. The app plays
# through a development ytmd built beside it, whose memory is its own
# process's: one already running serves instead; one started here is
# stopped at the end.
#
#   scripts/footprint.sh           the placeholder catalog, offline, silent
#   scripts/footprint.sh -live     the signed-in account, as thumbnails cost;
#                                  its songs play aloud
#   scripts/footprint.sh -daemon   ytmd alone instead, a placeholder one in a
#                                  home of its own: its memory and wake-ups
#                                  paused, playing, and after a Desktop toured
#                                  every page through it (skipped without a
#                                  window server); about 90 seconds, into
#                                  perf/footprint-daemon-latest.txt, against
#                                  perf/footprint-daemon-baseline.txt
#   scripts/footprint.sh -daemon -live
#                                  a real ytmd instead, signed out, its
#                                  engine playing two songs at volume 0
#   scripts/footprint.sh [-daemon] -update
#                                  make the latest run the baseline, after a
#                                  placeholder run of a deliberate change
set -eu

cd "$(dirname "$0")/.."

usage() {
	sed -n '2,27s/^# \{0,1\}//p' "$0" >&2
	exit 2
}

live=
update=
daemon=
while [ $# -gt 0 ]; do
	case $1 in
	-live) live=1 ;;
	-update) update=1 ;;
	-daemon) daemon=1 ;;
	*) usage ;;
	esac
	shift
done

latest=perf/footprint-latest.txt
baseline=perf/footprint-baseline.txt
if [ -n "$daemon" ]; then
	latest=perf/footprint-daemon-latest.txt
	baseline=perf/footprint-daemon-baseline.txt
fi

if [ -n "$update" ]; then
	# The account's catalog changes with the account: only the
	# placeholder's numbers compare from one machine and day to another.
	grep -q '^# source: placeholder$' "$latest" 2>/dev/null || {
		echo "footprint.sh: $latest is no placeholder run: run scripts/footprint.sh${daemon:+ -daemon} first" >&2
		exit 1
	}
	! grep -q '^# \(invalid\|failed\):' "$latest" || {
		echo "footprint.sh: $latest has invalid or failed phases: run it again" >&2
		exit 1
	}
	cp "$latest" "$baseline"
	echo "footprint.sh: $latest is $baseline now; commit it with the change it measures" >&2
	exit 0
fi

[ "$(uname -s)" = Darwin ] || {
	echo "footprint.sh: footprint(1), top's wake-ups and the harness are macOS only" >&2
	exit 1
}

header() {
	echo "# source: $source"
	echo "# date: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
	echo "# go: $(go version)"
	echo "# cpu: $(sysctl -n machdep.cpu.brand_string 2>/dev/null || uname -m), macOS $(sw_vers -productVersion)"
	echo "# git: $(git rev-parse --short HEAD 2>/dev/null || echo "no commit"), $(git status --porcelain 2>/dev/null | wc -l | tr -d ' ') files changed"
}

# An app bundle at $1, opened as the Finder would: macOS gives the focus
# to an app the user launches, and keeps it from one a shell starts.
build_app() {
	mkdir -p "$1/Contents/MacOS"
	go build -tags "memprof,$tags" -o "$1/Contents/MacOS/yt-music-player" ./cmd/desktop
	cat >"$1/Contents/Info.plist" <<'EOF'
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
}

# compare <keys…>: each phase's keys, baseline then latest.
compare() {
	echo
	awk -v keys="$*" '
		BEGIN { n = split(keys, key, " ") }
		$1 !~ /^phase=/ { next }
		{
			delete f
			for (i = 1; i <= NF; i++) {
				k = substr($i, 1, index($i, "=") - 1)
				f[k] = substr($i, index($i, "=") + 1)
			}
			line = ""
			for (i = 1; i <= n; i++) line = line (i > 1 ? SUBSEP : "") f[key[i]]
		}
		FNR == NR { base[f["phase"]] = line; next }
		f["phase"] in base {
			split(base[f["phase"]], b, SUBSEP)
			split(line, l, SUBSEP)
			if (!header++) {
				printf "%-20s", "phase"
				for (i = 1; i <= n; i++) printf " %17s", key[i]
				printf "\n"
			}
			printf "%-20s", f["phase"]
			for (i = 1; i <= n; i++) printf " %7s → %7s", b[i], l[i]
			printf "\n"
		}
	' "$baseline" "$latest"
}

# A run compares with a baseline of the same source; it returns 1 when
# there is none.
check_baseline() {
	if [ ! -s "$baseline" ]; then
		echo "footprint.sh: no $baseline to compare with: scripts/footprint.sh${daemon:+ -daemon} -update makes one" >&2
		return 1
	fi
	if ! grep -q "^# source: $source\$" "$baseline"; then
		echo "footprint.sh: $baseline measured another source; not compared" >&2
		return 1
	fi
}

source=placeholder
# The placeholder build tag swaps YouTube Music for the offline fake, in
# the app and in ytmd, which plays it silently on a clock.
tags=placeholder
[ -z "$live" ] || { source=live; tags=; }

# In /tmp, short: -daemon's socket, under a home in it, must fit in 103
# bytes.
tmp=$(mktemp -d /tmp/ytmd-footprint.XXXXXX)
ytm=$tmp/ytm
app=$tmp/YT\ Music\ Footprint.app
# spawned: this run started ytmd, and stops it.
spawned=
cleanup() {
	pkill -f "$app/Contents/MacOS/" 2>/dev/null || :
	[ -z "$spawned" ] || "$ytm" daemon stop >/dev/null 2>&1 || :
	rm -rf "$tmp"
}
trap cleanup EXIT
trap 'exit 1' INT TERM
go build -tags "$tags" -o "$tmp/ytmd" ./cmd/ytmd
go build -tags "$tags" -o "$ytm" ./cmd/ytm
YTMD_PATH=$tmp/ytmd
export YTMD_PATH

if [ -n "$daemon" ]; then
	# A window server: a GUI login's (Aqua), not an ssh one's.
	desktop=
	[ "$(launchctl managername 2>/dev/null)" != Aqua ] || desktop=1
	[ -z "$desktop" ] || build_app "$app"
	{
		echo "# ytmd footprint, scripts/footprint.sh -daemon"
		header
		echo "# phases: one line each, key=value; MB: footprint(1)'s, RSS-MB: ps's, sampled twice a second; idle-wakeups and csw (context switches): top's, over the phase"
	} >"$tmp/out.txt"

	# A home of its own: a fresh state (the placeholder's queue, paused),
	# and no other development ytmd's socket, lock or log. Nor the login
	# Keychain, whose search list is under $HOME: a real ytmd plays signed
	# out, never asking for the Desktop's item.
	mkdir "$tmp/home"
	HOME=$tmp/home TMPDIR=$tmp
	export HOME TMPDIR
	if [ -n "$live" ]; then
		# A real build has no seed: two songs to play, silently.
		state="$HOME/Library/Application Support/yt-music-player"
		mkdir -p "$state"
		cat >"$state/daemon-dev.json" <<'EOF'
{"v":1,"queue":["dQw4w9WgXcQ","fJ9rUzIMcZQ"],"order":[0,1],"volume":0,"songs":[{"id":"dQw4w9WgXcQ","title":"Never Gonna Give You Up","artist":"Rick Astley","seconds":213},{"id":"fJ9rUzIMcZQ","title":"Bohemian Rhapsody","artist":"Queen","seconds":359}]}
EOF
	fi
	spawned=1
	"$ytm" daemon start >/dev/null
	pid=$(pgrep -f "^$tmp/ytmd\$")
	secs=10

	# measure <phase> <playing: yes or no> samples ytmd's footprint and
	# RSS twice a second for $secs seconds while top counts its wake-ups.
	# A phase whose ytmd did not play as it should is invalid; a paused
	# one that woke the CPU from idle fails.
	measure() {
		was=$("$ytm" status --json | sed -n 's/.*"state":"\([a-z]*\)".*/\1/p')
		# Waits out ytmd's 2 s save, and its free of the heap 3 s after
		# the last request (internal/daemon), this status's too.
		sleep 5
		top -l 2 -s "$secs" -pid "$pid" -stats pid,idlew,csw >"$tmp/top.txt" 2>&1 &
		top=$!
		: >"$tmp/mb.txt"
		i=0
		while [ "$i" -lt $((secs * 2)) ]; do
			fp=$(footprint -f bytes -p "$pid" | sed -n 's/.*Footprint: \([0-9]*\) B.*/\1/p')
			echo "$fp $(ps -o rss= -p "$pid")" >>"$tmp/mb.txt"
			sleep 0.5
			i=$((i + 1))
		done
		wait "$top"
		now=$("$ytm" status --json | sed -n 's/.*"state":"\([a-z]*\)".*/\1/p')
		# top's counts are since the process started, "+" marking a rise.
		wakes=$(awk -v pid="$pid" '
			function num(s) {
				sub(/[+-]$/, "", s)
				if (s ~ /K$/) return s * 1000
				if (s ~ /M$/) return s * 1000000
				return s + 0
			}
			$1 == pid { if (!seen++) { i0 = num($2); c0 = num($3) } i1 = num($2); c1 = num($3) }
			END { if (seen < 2) print "? ?"; else print i1 - i0, c1 - c0 }
		' "$tmp/top.txt")
		awk -v phase="$1" -v want="$2" -v was="$was" -v now="$now" -v secs="$secs" -v wakes="$wakes" '
			{
				mb = $1 / 1048576; rss = $2 / 1024
				if (!n || mb < lo) lo = mb
				if (!n || mb > hi) hi = mb
				sum += mb; rsum += rss; n++
			}
			END {
				split(wakes, w, " ")
				playing = was == "playing" && now == "playing" ? "yes" : was != "playing" && now != "playing" ? "no" : "part"
				printf "phase=%s playing=%s secs=%d MB-min=%.1f MB-mean=%.1f MB-max=%.1f RSS-MB-mean=%.1f idle-wakeups=%s csw=%s\n",
					phase, playing, secs, lo, sum / n, hi, rsum / n, w[1], w[2]
				if (playing != want) printf "# invalid: %s: playing=%s, want %s\n", phase, playing, want
				if (want == "no" && w[1] != "0") printf "# failed: %s: %s idle wake-ups while paused, want 0\n", phase, w[1]
			}
		' "$tmp/mb.txt" >>"$tmp/out.txt"
	}

	echo "footprint.sh: measuring ytmd ($source), about $([ -n "$desktop" ] && echo 90 || echo 50) seconds" >&2
	measure ytmd-idle no
	"$ytm" play >/dev/null
	measure ytmd-playing yes
	"$ytm" pause >/dev/null
	measure ytmd-paused no

	if [ -n "$desktop" ]; then
		# The memprof harness tours every page three times, playing as it
		# goes, then pauses: ytmd serves each page's fetch. In the
		# background (-g), so no focus is taken; port 0: no clash with
		# another memprof build's pprof (docs/performance.md).
		open -n -g -F --env YT_HARNESS=1 --env YT_PPROF_ADDR=127.0.0.1:0 \
			--env HOME="$HOME" --env TMPDIR="$TMPDIR" --env YTMD_PATH="$YTMD_PATH" \
			--stdout /dev/null --stderr "$tmp/log.txt" "$app"
		started=$(date +%s)
		until grep -q '^HARNESS DONE' "$tmp/log.txt" 2>/dev/null; do
			if [ $(($(date +%s) - started)) -gt 180 ]; then
				echo "footprint.sh: the Desktop's tour did not finish; its log ends:" >&2
				tail -n 20 "$tmp/log.txt" >&2 2>/dev/null || :
				exit 1
			fi
			sleep 1
		done
		toured=$(($(date +%s) - started))
		# SIGTERM, not an Apple Event, which would ask for the right to
		# control the app: ytmd sees the connection end either way.
		pkill -f "$app/Contents/MacOS/" || :
		while pgrep -f "$app/Contents/MacOS/" >/dev/null; do sleep 0.2; done
		measure ytmd-after-desktop no
		echo "# desktop: toured every page three times in ${toured} s, then quit" >>"$tmp/out.txt"
	else
		echo "# desktop: skipped (no window server)" >>"$tmp/out.txt"
	fi
	echo "# end" >>"$tmp/out.txt"

	mkdir -p perf
	mv "$tmp/out.txt" "$latest"
	cat "$latest"
	status=0
	if grep -q '^# invalid:' "$latest"; then
		status=1
		echo "footprint.sh: invalid phases (see '# invalid'): run again" >&2
	fi
	if grep -q '^# failed:' "$latest"; then
		status=1
		echo "footprint.sh: ytmd woke while paused (see '# failed'): idle must wake nothing" >&2
	fi
	! check_baseline || compare MB-mean RSS-MB-mean idle-wakeups
	exit "$status"
fi

# Exit code 3: none running, so the app starts this one.
"$ytm" daemon status >/dev/null 2>&1 || [ $? != 3 ] || spawned=1

build_app "$app"
echo "footprint.sh: measuring ($source), about 70 seconds: click the window if it stays behind, then hands off" >&2
{
	echo "# yt-music-player footprint, scripts/footprint.sh"
	header
} >"$tmp/out.txt"

# Port 0: no clash with another memprof build's pprof (docs/performance.md).
open -n -F -W --env YT_HARNESS=footprint --env MYGO_FRAME_STATS=all \
	--env YT_PPROF_ADDR=127.0.0.1:0 --env YTMD_PATH="$YTMD_PATH" \
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

! check_baseline || compare frames/min MB-mean gfx-MB-mean
[ -z "$invalid" ]
