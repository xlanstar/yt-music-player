#!/bin/sh
# Builds this checkout and installs it with Homebrew, through the release's
# cask (packaging/homebrew/yt-music-player.rb) from a local tap: the app in
# /Applications, ytm on the PATH, the background player stopped before a
# reinstall. macOS only.
#
#   scripts/install.sh [-universal]
#
#   -universal   Apple silicon and Intel, as releases are; this Mac's by
#                default
#
# brew uninstall --cask local/yt-music-player/yt-music-player removes it.
set -eu

cd "$(dirname "$0")/.."

usage() {
	sed -n '2,12s/^# \{0,1\}//p' "$0" >&2
	exit 2
}

die() {
	echo "install.sh: $*" >&2
	exit 1
}

[ $# -le 1 ] || usage
case ${1-} in
'') platform=darwin/$(go env GOARCH) arch="-$(go env GOARCH)" ;;
-universal) platform=darwin/universal arch= ;;
*) usage ;;
esac
[ "$(uname)" = Darwin ] || die "macOS only"
command -v brew >/dev/null || die "needs Homebrew"

# Never clean up the user's other packages, as brew install does by default.
export HOMEBREW_NO_INSTALL_CLEANUP=1 HOMEBREW_NO_ENV_HINTS=1

tap=local/yt-music-player
cask=$tap/yt-music-player

scripts/build.sh -platform "$platform"

slug=$(jq -r .slug mygo.json)
version=$(jq -r .version mygo.json)
dmg="$PWD/build/$(echo "$platform" | tr / -)/$slug-$version$arch.dmg"
[ -f "$dmg" ] || die "no $dmg"
sha=$(shasum -a 256 "$dmg" | cut -d ' ' -f 1)

tapdir=$(brew --repository)/Library/Taps/local/homebrew-yt-music-player
[ -d "$tapdir" ] || brew tap-new --no-git "$tap" >/dev/null
mkdir -p "$tapdir/Casks"
# The release's cask, but for its header, from the local disk image; the
# commit in its version tells what is installed (brew info).
sed -e '1,/^cask /{/^#/d;}' \
	-e "s/@VERSION@/$version,$(git rev-parse --short HEAD)/" \
	-e "s/@SHA256@/$sha/" \
	-e "s|^  url .*|  url \"file://$(echo "$dmg" | sed 's/ /%20/g')\"|" \
	packaging/homebrew/yt-music-player.rb >"$tapdir/Casks/yt-music-player.rb"
! grep -n '@[A-Z0-9_]*@' "$tapdir/Casks/yt-music-player.rb" || die "the cask has a placeholder left"

if brew list --cask "$cask" >/dev/null 2>&1; then
	brew reinstall --cask "$cask"
else
	brew install --cask "$cask"
fi

# Built here, signed ad hoc and not notarized: Gatekeeper would refuse it
# once brew quarantined it.
ytm=$(readlink "$(brew --prefix)/bin/ytm")
xattr -dr com.apple.quarantine "${ytm%/Contents/Resources/ytm}"
echo "Installed ${ytm%/Contents/Resources/ytm} and $(brew --prefix)/bin/ytm"
