#!/bin/sh
# Builds the Desktop, ytmd and ytm as releases build them (scripts/build.sh,
# mygo build) for every platform released, into perf/binsize-latest.txt,
# and compares their sizes with perf/binsize-baseline.txt. Sizes are the
# linker's: darwin's carry its ad-hoc signature, not codesign's (about
# 90 KB less on the Desktop), and Windows' Desktop lacks mygo's icon and
# manifest resources. The same Go and code give the same bytes on any
# machine.
#
#   scripts/binsize.sh                  every platform released
#   scripts/binsize.sh -platform darwin/arm64,linux/amd64
#                                       those platforms only
#   scripts/binsize.sh -against <rev>   compare with git revision <rev>
#                                       instead of the baseline, built in a
#                                       temporary worktree, into
#                                       perf/binsize-against.txt
#   scripts/binsize.sh -update          make perf/binsize-latest.txt the
#                                       baseline, after a run of every
#                                       platform
set -eu

cd "$(dirname "$0")/.."

usage() {
	sed -n '2,19s/^# \{0,1\}//p' "$0" >&2
	exit 2
}

# darwin/universal ships darwin/arm64 and darwin/amd64 merged.
released="darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64 windows/arm64"
latest=perf/binsize-latest.txt
baseline=perf/binsize-baseline.txt
compared=perf/binsize-against.txt

platforms=
against=
update=
while [ $# -gt 0 ]; do
	case $1 in
	-update) update=1 ;;
	-platform | -against)
		[ $# -ge 2 ] || usage
		case $1 in
		-platform) platforms=$(echo "$2" | tr , ' ') ;;
		-against) against=$2 ;;
		esac
		shift
		;;
	*) usage ;;
	esac
	shift
done

die() {
	echo "binsize.sh: $*" >&2
	exit 1
}

if [ -n "$update" ]; then
	[ -z "$platforms$against" ] || die "-update takes no other flag"
	[ -f "$latest" ] || die "no $latest: run scripts/binsize.sh first"
	grep -q '^# platforms: released$' "$latest" ||
		die "$latest has not every platform: run scripts/binsize.sh without -platform"
	cp "$latest" "$baseline"
	echo "binsize.sh: $latest is the baseline now; commit it with the change it measures" >&2
	exit 0
fi

scope=released
if [ -n "$platforms" ]; then
	scope=partial
	for p in $platforms; do
		case " $released " in
		*" $p "*) ;;
		*) die "-platform $p: released are $released" ;;
		esac
	done
else
	platforms=$released
fi
if [ -n "$against" ]; then
	git rev-parse --verify --quiet "$against^{commit}" >/dev/null ||
		die "-against $against: no such git revision"
fi

# Releases build with Go's defaults for each architecture.
unset GOAMD64 GOARM64

# sizes <tree> <git line> writes the sizes of the tree's builds, one line
# each: program, platform, bytes.
sizes() {
	tree=$1
	version=$(jq -r .version "$tree/mygo.json")
	# What mygo build links into the Desktop (cmd/mygo/build.go, v0.5.5):
	# what mygo.json says of the app, the update feed and production.
	desktop_ldflags=$(jq -r '
		def quote: if test("[ \t\"'"'"']") then "'"'"'" + . + "'"'"'" else . end;
		[ ["Name", .name], ["Version", .version], ["Identifier", .identifier] ]
		| map(select(.[1] != null and .[1] != "")
			| " -X " + ("github.com/egoist/mygo.package" + .[0] + "=" + .[1] | quote))
		| join("")' "$tree/mygo.json")
	feed=$(jq -r '
		if .updates == null then ""
		elif (.updates.github // "") == "" then (.updates.url | rtrimstr("/")) + "/"
		elif (.updates.tagPrefix // "v") != "v" then error("a tagged feed")
		else "https://github.com/" + .updates.github + "/releases/latest/download/"
		end' "$tree/mygo.json") || die "$tree/mygo.json: updates binsize.sh does not know"
	key=$(jq -r '.updates.publicKey // ""' "$tree/mygo.json")
	{
		echo "# yt-music-player binary sizes, scripts/binsize.sh"
		echo "# platforms: $scope"
		echo "# date: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
		echo "# go: $(cd "$tree" && go env GOVERSION)"
		echo "# git: $2"
		echo "# format: program, platform, bytes"
	}
	for p in $platforms; do
		goos=${p%/*} goarch=${p#*/}
		ldflags=$desktop_ldflags
		[ -z "$feed" ] || ldflags="$ldflags -X github.com/egoist/mygo.packageUpdateFeed=${feed}update-$goos-$goarch.json -X github.com/egoist/mygo.packageUpdateKey=$key"
		ldflags="$ldflags -X github.com/egoist/mygo.production=1"
		helper=
		if [ "$goos" = windows ]; then
			# GUI programs, without a console window.
			ldflags="$ldflags -H=windowsgui"
			helper=-H=windowsgui
		fi
		for prog in desktop ytmd ytm; do
			out=$tmp/$prog-$goos-$goarch
			case $prog in
			desktop) set -- -tags mygo_noinspector -ldflags "-s -w$ldflags" ;;
			ytmd) set -- -ldflags "-s -w -X yt-music-player/internal/version.Version=$version${helper:+ $helper}" ;;
			ytm) set -- -ldflags "-s -w -X yt-music-player/internal/version.Version=$version" ;;
			esac
			(cd "$tree" && GOOS=$goos GOARCH=$goarch CGO_ENABLED=0 go build -trimpath "$@" -o "$out" "./cmd/$prog") ||
				die "building $prog for $p failed"
			echo "$prog $p $(wc -c <"$out" | tr -d ' ')"
			rm -f "$out"
		done
	done
}

# compare <old> <new> <old's name>: each program and platform, and the
# three together, as an app ships them.
compare() {
	old_go=$(sed -n 's/^# go: //p' "$1")
	new_go=$(sed -n 's/^# go: //p' "$2")
	[ "$old_go" = "$new_go" ] ||
		echo "binsize.sh: warning: $3 was built with ${old_go:-unknown Go}, this tree with $new_go: the toolchain moves sizes too" >&2
	echo
	awk -v name="$3" '
		function kb(b) { return sprintf("%.1f", b / 1024) }
		function row(key, o, n) {
			d = n - o
			printf "%-8s %-14s %10s KB %10s KB %+10d B %+7.2f%%\n", key[1], key[2], kb(o), kb(n), d, o ? 100 * d / o : 0
		}
		/^#/ { next }
		FNR == NR { old[$1 " " $2] = $3; next }
		($1 " " $2) in old {
			if (!header++) printf "%-8s %-14s %13s %13s %12s\n", "program", "platform", name, "latest", "delta"
			split($1 " " $2, key, " ")
			row(key, old[$1 " " $2], $3)
			if (!($2 in seen)) { seen[$2] = 1; order[++n] = $2 }
			ot[$2] += old[$1 " " $2]; nt[$2] += $3
		}
		END {
			for (i = 1; i <= n; i++) {
				key[1] = "all"; key[2] = order[i]
				row(key, ot[order[i]], nt[order[i]])
			}
		}
	' "$1" "$2"
}

tmp=$(mktemp -d)
old=
cleanup() {
	[ -z "$old" ] || git worktree remove --force "$old" 2>/dev/null || :
	rm -rf "$tmp"
}
trap cleanup EXIT
trap 'exit 1' INT TERM

mkdir -p perf
echo "binsize.sh: building $(echo "$platforms" | wc -w | tr -d ' ') platforms" >&2
sizes . "$(git rev-parse --short HEAD), $(git status --porcelain | wc -l | tr -d ' ') files changed" >"$tmp/latest.txt"
mv "$tmp/latest.txt" "$latest"

if [ -n "$against" ]; then
	old=$tmp/against
	git worktree add --quiet --detach "$old" "$against"
	echo "binsize.sh: building $against" >&2
	sizes "$old" "$(git rev-parse --short "$against^{commit}"), -against $against" >"$tmp/against.txt"
	mv "$tmp/against.txt" "$compared"
	compare "$compared" "$latest" "$against"
elif [ -s "$baseline" ]; then
	compare "$baseline" "$latest" baseline
else
	cat "$latest"
	echo "binsize.sh: no $baseline to compare with: scripts/binsize.sh -update makes one" >&2
fi
