#!/bin/sh
# Runs the benchmark suite into perf/benchmarks-latest.txt and compares it with
# perf/benchmarks-baseline.txt. Time per op is too noisy to gate in tests, which
# hold allocations instead (TestBudget*): compare allocs first, then
# time on a quiet machine.
#
#   scripts/bench.sh                   the suite, 6 runs of 100 ms each
#   scripts/bench.sh ./internal/artwork/ ...
#                                      every benchmark of those packages
#   scripts/bench.sh -run Pages/Home   benchmarks matching a -bench pattern,
#                                      in every package
#   scripts/bench.sh -count 10 ...     10 runs instead of 6
#   scripts/bench.sh -against <rev> ...
#                                      compare with git revision <rev> instead
#                                      of the baseline, built alongside and run
#                                      in turn with this tree, into
#                                      perf/benchmarks-against.txt
#   scripts/bench.sh -profile <package> [-run pattern]
#                                      also profile CPU and allocations into
#                                      perf/cpu.prof and perf/mem.prof
#   scripts/bench.sh -update           make perf/benchmarks-latest.txt the baseline,
#                                      after a full run of a deliberate change
#
# The suite also runs the RunParallel benchmarks at 1, 4 and every CPU;
# with -run or packages, everything runs in one pass at every CPU.
set -eu

cd "$(dirname "$0")/.."

# Pinned, so that every machine computes the same statistics.
benchstat=golang.org/x/perf/cmd/benchstat@v0.0.0-20261009192801-be2c69fb417e
latest=perf/benchmarks-latest.txt
baseline=perf/benchmarks-baseline.txt
compared=perf/benchmarks-against.txt

usage() {
	sed -n '2,25s/^# \{0,1\}//p' "$0" >&2
	exit 2
}

# Six runs are the fewest benchstat gives a 95% confidence interval for:
# its comparison gains from more runs, not longer ones, and most ops take
# microseconds, which 100 ms repeats thousands of times.
count=6
benchtime=100ms
filter=
pkgs=
update=
against=
profile=
while [ $# -gt 0 ]; do
	case $1 in
	-update) update=1 ;;
	-profile) profile=1 ;;
	-run | -count | -against)
		[ $# -ge 2 ] || usage
		case $1 in
		-run) filter=$2 ;;
		-count)
			case $2 in '' | *[!0-9]* | 0) usage ;; esac
			count=$2
			;;
		-against) against=$2 ;;
		esac
		shift
		;;
	-h | -help | --help) usage ;;
	-*) usage ;;
	*) pkgs="$pkgs $1" ;;
	esac
	shift
done

die() {
	echo "bench.sh: $*" >&2
	exit 1
}

if [ -n "$update" ]; then
	[ -z "$filter$pkgs$against$profile" ] || die "-update takes no other flag or package"
	[ -f "$latest" ] || die "no $latest: run scripts/bench.sh first"
	# A partial run would leave the baseline without the rest.
	grep -q '^# suite: full$' "$latest" ||
		die "$latest is not a full run of the suite: run scripts/bench.sh without filters"
	cp "$latest" "$baseline"
	echo "bench.sh: $latest is the baseline now; commit it with the change it measures" >&2
	exit 0
fi

if [ -n "$profile" ]; then
	[ -z "$against" ] || die "-profile profiles this tree alone: drop -against"
	# shellcheck disable=SC2086 # The packages are words.
	set -- $pkgs
	[ $# = 1 ] || die "-profile needs exactly one package (go test profiles one at a time), as in scripts/bench.sh -profile ./internal/job/"
fi
if [ -n "$against" ]; then
	git rev-parse --verify --quiet "$against^{commit}" >/dev/null ||
		die "-against $against: no such git revision"
fi

# Time per op suffers from whatever else runs: say so, rather than let a
# busy machine's numbers pass for a regression or an improvement.
cpus=$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 1)
load=$(uptime | sed 's/.*load average[s]*: *//; s/,//g')
if echo "$load $cpus" | awk '{ exit !($1 > ($4 / 4 > 1 ? $4 / 4 : 1)) }'; then
	echo "bench.sh: warning: load average $load on $cpus CPUs: the machine looks busy, ns/op will be noisy" >&2
fi

case $(uname -s) in
Darwin) cpu=$(sysctl -n machdep.cpu.brand_string 2>/dev/null || uname -m) ;;
*) cpu=$(sed -n 's/^model name[[:space:]]*: //p' /proc/cpuinfo 2>/dev/null | head -n 1) ;;
esac
commit=$(git rev-parse --short HEAD 2>/dev/null || echo "no commit")
changed=$(git status --porcelain 2>/dev/null | wc -l | tr -d ' ')

suite=full
[ -z "$filter$pkgs" ] || suite=partial
mkdir -p perf
tmp=$(mktemp -d)
old=
keep=
cleanup() {
	if [ -n "$old" ]; then
		git worktree remove --force "$old" 2>/dev/null || :
		git worktree prune
	fi
	[ -n "$keep" ] || rm -rf "$tmp"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

# RunParallel benchmarks: at one GOMAXPROCS they hide how contention
# scales, so the suite runs them on their own at several.
parallel='^BenchmarkAuthSignedIn$|^BenchmarkResolve$/^cached_parallel$'
procs=1,4
[ "$cpus" = 1 ] || [ "$cpus" = 4 ] || procs=$procs,$cpus

# passes calls $1 with each pass's -bench pattern, -skip pattern, -cpu
# list and packages.
passes() {
	if [ -n "$pkgs" ]; then
		# shellcheck disable=SC2086 # The packages are words.
		$1 "${filter:-.}" '' '' $pkgs
	elif [ -n "$filter" ]; then
		$1 "$filter" '' '' . ./internal/...
	else
		# The app's own are the screens' frames and interactions, and the
		# memoized values remade (real work); its helpers take
		# nanoseconds, which the budgets hold.
		$1 '^Benchmark(Pages|Interactions|Memo)$' '' '' .
		$1 . "$parallel" '' ./internal/...
		$1 "$parallel" '' "$procs" ./internal/innertube/ ./internal/stream/
	fi
}

# Lines that start with '#' are no benchmark configuration to benchstat,
# so that runs of two commits still compare.
header() {
	echo "# yt-music-player benchmarks, scripts/bench.sh -count $count -benchtime $benchtime$1"
	echo "# suite: $suite"
	echo "# date: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
	echo "# go: $(go version)"
	echo "# cpu: $cpu, $cpus CPUs"
	echo "# git: $commit, $changed files changed"
	echo "# load: $load"
}

# The -against mode: the committed baseline measures its day, machine state
# and Go toolchain as much as the code (Go 1.27's allocator alone moved the
# allocation-heavy numbers), so build <rev> here and now instead, and
# alternate the two trees' runs so that drift (heat, load) hits both.

# tree prints the root of the old or new tree.
tree() {
	if [ "$1" = old ]; then echo "$old"; else pwd; fi
}

# compile builds, once, the test binaries of a pass's packages in both
# trees, listing them in $tmp/pass<n>.<tree> as "import path, directory".
# A package <rev> lacks has no old rows.
compile() {
	npass=$((npass + 1))
	shift 3
	for t in old new; do
		list=$tmp/pass$npass.$t
		# -e: a package the old tree lacks is no error.
		e=
		[ "$t" = new ] || e=-e
		# shellcheck disable=SC2086 # Empty or one word.
		(cd "$(tree "$t")" && go list $e -f '{{if and (not .Error) (or .TestGoFiles .XTestGoFiles)}}{{.ImportPath}} {{.Dir}}{{end}}' "$@") >"$list"
		while read -r pkg dir; do
			bin=$tmp/$t.bin/$(echo "$pkg" | tr / _).test
			[ ! -f "$bin" ] || continue
			echo "bench.sh: building $pkg ($t)" >&2
			(cd "$(tree "$t")" && go test -c -o "$bin" "$pkg" </dev/null) ||
				die "$pkg does not build in the $t tree"
		done <"$list"
	done
}

# run1 runs package $2's benchmarks of this pass once in tree $1.
run1() {
	bin=$tmp/$1.bin/$(echo "$2" | tr / _).test
	dir=$(awk -v p="$2" '$1 == p { print substr($0, length(p) + 2) }' "$tmp/pass$npass.$1")
	[ -n "$dir" ] || return 0
	(cd "$dir" && "$bin" -test.run '^$' -test.bench "$pattern" -test.benchmem \
		-test.count 1 -test.benchtime "$benchtime" \
		${skip:+"-test.skip=$skip"} ${procs1:+"-test.cpu=$procs1"}) </dev/null >"$tmp/run.txt" 2>"$tmp/run.err" || {
		cat "$tmp/run.txt" "$tmp/run.err" >&2
		die "$2 failed in the $1 tree"
	}
	cat "$tmp/run.txt" >>"$tmp/$1.txt"
}

# round runs each package of a pass once in each tree, the old one first
# in odd rounds, so that neither always runs second.
round() {
	npass=$((npass + 1))
	pattern=$1 skip=$2 procs1=$3
	first=old second=new
	[ $((i % 2)) = 1 ] || first=new second=old
	cut -d ' ' -f 1 "$tmp/pass$npass.new" "$tmp/pass$npass.old" | awk '!seen[$0]++' >"$tmp/pkgs"
	while read -r pkg; do
		run1 "$first" "$pkg"
		run1 "$second" "$pkg"
	done <"$tmp/pkgs"
}

if [ -n "$against" ]; then
	old=$tmp/old
	git worktree add --quiet --detach "$old" "$against" >&2
	mkdir "$tmp/old.bin" "$tmp/new.bin"
	: >"$tmp/old.txt"
	: >"$tmp/new.txt"
	npass=0
	passes compile
	i=1
	while [ $i -le "$count" ]; do
		echo "bench.sh: run $i of $count, $against and this tree in turn" >&2
		npass=0
		passes round
		i=$((i + 1))
	done
	{
		header ", -against $against ($(git rev-parse --short "$against^{commit}"))"
		go run "$benchstat" old="$tmp/old.txt" new="$tmp/new.txt"
	} >"$tmp/against.txt"
	mv "$tmp/against.txt" "$compared"
	cat "$compared"
	exit 0
fi

header '' >"$tmp/out.txt"

# bench runs the benchmarks matching $1, skipping those matching $2, at
# the GOMAXPROCS listed in $3, in the packages that follow, appending
# their results and showing each package's outcome.
bench() {
	pattern=$1 skip=$2 procs1=$3
	shift 3
	[ -z "$procs1" ] || set -- -cpu "$procs1" "$@"
	[ -z "$skip" ] || set -- -skip "$skip" "$@"
	[ -z "$profile" ] || set -- -cpuprofile "$PWD/perf/cpu.prof" -memprofile "$PWD/perf/mem.prof" -o "$tmp/profile.test" "$@"
	echo "bench.sh: $* -bench '$pattern' -count $count -benchtime $benchtime" >&2
	{
		go test -run '^$' -bench "$pattern" -benchmem -count "$count" -benchtime "$benchtime" "$@"
		echo $? >"$tmp/status"
	} | tee -a "$tmp/out.txt" | grep -E '^(ok|FAIL|panic|--- FAIL)' >&2 || :
	[ "$(cat "$tmp/status")" = 0 ] || {
		keep=1
		die "go test failed; see $tmp/out.txt"
	}
}

passes bench
mv "$tmp/out.txt" "$latest"

if [ -n "$profile" ]; then
	echo "bench.sh: profiles of the runs: go tool pprof -top perf/cpu.prof; go tool pprof -alloc_objects -top perf/mem.prof (-http=: for a browser)" >&2
fi

if [ ! -f "$baseline" ]; then
	echo "bench.sh: no $baseline to compare with: scripts/bench.sh -update makes one" >&2
	go run "$benchstat" "$latest"
	exit 0
fi

# A baseline of other settings or another Go measures those as much as
# the code: say so before its numbers are read as the change's.
for field in \
	's/^# yt-music-player benchmarks, .*\(-count [^ ]*\).*/\1/p' \
	's/^# yt-music-player benchmarks, .*\(-benchtime [^ ,]*\).*/\1/p' \
	's/^# go: go version /Go /p' \
	's/^# cpu: /CPU /p'; do
	was=$(sed -n "$field" "$baseline" | head -n 1)
	now=$(sed -n "$field" "$latest" | head -n 1)
	[ "$was" = "$now" ] ||
		echo "bench.sh: warning: the baseline ran with ${was:-unknown settings}, this run with $now: -against <rev> compares like with like" >&2
done

# Rows benchstat would show alone: benchmarks new since the baseline,
# and, after a full run, those gone since. Either makes it stale. A
# benchmark none of whose rows match shows as its name alone.
awk -v full="$([ $suite = full ] && echo 1)" '
	function top(row) {
		sub(/\/.*/, "", row)
		sub(/-[0-9]+$/, "", row)
		return substr(row, 10)
	}
	function list(what, rows, n, other, othertop,   i, k, f, pkg, t, said, out) {
		for (i = 1; i <= n; i++) {
			k = rows[i]
			if (k in other) continue
			split(k, f, SUBSEP)
			if (f[1] != pkg) {
				if (out != "") out = out "\n"
				pkg = f[1]
				out = out "  " pkg ":"
			}
			t = top(f[2])
			if (!((pkg, t) in othertop)) {
				if (!((pkg, t) in said)) out = out " " t
				said[pkg, t] = 1
			} else out = out " " substr(f[2], 10)
		}
		if (out != "") printf "bench.sh: warning: %s:\n%s\n", what, out
	}
	$1 == "pkg:" { pkg = $2 }
	$1 !~ /^Benchmark/ || (FNR == NR ? (pkg, $1) in ran : (pkg, $1) in was) { next }
	FNR == NR { ran[pkg, $1] = 1; rantop[pkg, top($1)] = 1; ranrow[++nran] = pkg SUBSEP $1 }
	FNR != NR { was[pkg, $1] = 1; wastop[pkg, top($1)] = 1; wasrow[++nwas] = pkg SUBSEP $1 }
	END {
		list("no baseline rows for these (scripts/bench.sh -update records them)", ranrow, nran, was, wastop)
		if (full) list("baseline rows of benchmarks that no longer ran", wasrow, nwas, ran, rantop)
	}
' "$latest" "$baseline" >&2

# The baseline's results of the benchmarks that ran, by package, so that
# a partial run compares with them alone.
awk '
	FNR == NR {
		if ($1 == "pkg:") pkg = $2
		else if ($1 ~ /^Benchmark/) ran[pkg, $1] = 1
		next
	}
	$1 == "pkg:" { pkg = $2 }
	$1 !~ /^Benchmark/ || (pkg, $1) in ran
' "$latest" "$baseline" >"$tmp/baseline.txt"
go run "$benchstat" baseline="$tmp/baseline.txt" latest="$latest"
