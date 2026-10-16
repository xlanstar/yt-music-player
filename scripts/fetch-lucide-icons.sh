#!/bin/sh
# Fetches Lucide icons (https://lucide.dev, ISC: cmd/desktop/icons/LICENSE)
# into cmd/desktop/icons/<name>.svg, named as Lucide names them, then
# compresses them with the svg-compress hook of prek.toml.
#
#   scripts/fetch-lucide-icons.sh               every icon already there, again
#   scripts/fetch-lucide-icons.sh house x ...   those icons
set -eu

cd "$(dirname "$0")/.."

# Pinned, so that fetching again changes nothing until the bump.
version=1.57.0
dir=cmd/desktop/icons

if [ $# -eq 0 ]; then
	for f in "$dir"/*.svg; do
		set -- "$@" "$(basename "$f" .svg)"
	done
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
# Straight from the npm registry, so that fetching needs no Node.
curl -fsSL "https://registry.npmjs.org/lucide-static/-/lucide-static-$version.tgz" | tar -xzf - -C "$tmp"

files=
for name in "$@"; do
	cp "$tmp/package/icons/$name.svg" "$dir/"
	files="$files $dir/$name.svg"
done
cp "$tmp/package/LICENSE" "$dir/"

# The hook fails when it rewrites a file, as it must here; run again, it
# passes unless svgo itself failed.
# shellcheck disable=SC2086
prek run svg-compress --files $files >/dev/null || prek run svg-compress --files $files
