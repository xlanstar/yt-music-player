#!/bin/sh
# Builds ytmd into build/dev/ and runs a front-end from source with
# YTMD_PATH pointing at it, so the front-end starts this ytmd rather than
# look for one next to `go run`'s temporary binary:
#
#   scripts/dev.sh [-placeholder] [desktop|ytm] [args…]
#
#   -placeholder   both built with -tags placeholder: the offline fake,
#                  which plays silently on a clock
#   desktop        go run ./cmd/desktop (the default)
#   ytm            go run ./cmd/ytm
#   args           passed on to the front-end
#
# Development builds use their own socket, state and log (the "-dev"
# suffix), so an installed ytmd is left alone. A ytmd already running from
# an earlier build keeps serving: stop it first (ytm daemon stop) to run
# the new one.
set -eu

cd "$(dirname "$0")/.."

usage() {
	sed -n '2,17s/^# \{0,1\}//p' "$0" >&2
	exit 2
}

tags=
if [ "${1:-}" = -placeholder ]; then
	tags=placeholder
	shift
fi
frontend=desktop
case ${1:-} in
desktop | ytm)
	frontend=$1
	shift
	;;
-*) usage ;;
esac

ytmd="build/dev/ytmd$(go env GOEXE)"
go build -tags "$tags" -o "$ytmd" ./cmd/ytmd
YTMD_PATH="$(pwd)/$ytmd"
export YTMD_PATH
exec go run -tags "$tags" "./cmd/$frontend" "$@"
