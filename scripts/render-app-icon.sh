#!/bin/sh
# Renders the app icon from its one source, assets/icon/icon.svg, into
# resources/icon.png (what `mygo build` bundles) and assets/icon/ (PNGs of
# every size, .icns, .ico). Kept out of resources/, whose every file ships
# inside the app. Needs rsvg-convert and ImageMagick (brew install librsvg
# imagemagick) and macOS's iconutil.
set -eu

cd "$(dirname "$0")/.."

src=assets/icon/icon.svg
out=assets/icon
png=$out/png
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

render() { rsvg-convert -w "$1" -h "$1" "$src" -o "$2"; }

rm -rf "$png"
mkdir -p "$png"
for s in 16 32 48 64 128 256 512 1024; do
	render "$s" "$png/icon-$s.png"
done
cp "$png/icon-1024.png" resources/icon.png

# macOS: the iconset's names are fixed by iconutil.
set_=$tmp/icon.iconset
mkdir "$set_"
for s in 16 32 128 256 512; do
	render "$s" "$set_/icon_${s}x${s}.png"
	render $((s * 2)) "$set_/icon_${s}x${s}@2x.png"
done
iconutil -c icns "$set_" -o "$out/icon.icns"

# Windows and favicons.
magick "$png/icon-16.png" "$png/icon-32.png" "$png/icon-48.png" \
	"$png/icon-64.png" "$png/icon-128.png" "$png/icon-256.png" "$out/icon.ico"
