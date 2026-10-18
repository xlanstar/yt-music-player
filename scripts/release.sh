#!/bin/sh
# Releases the app (docs/release.md).
#
#   scripts/release.sh patch|minor|major
#       From a clean main equal to origin/main: runs the checks, moves
#       CHANGELOG.md's Unreleased section under the next version, sets it in
#       mygo.json, commits, tags vX.Y.Z and pushes main and the tag at once.
#       The Release workflow builds the tag into a draft GitHub release,
#       then its publish job, once approved, runs publish.
#   scripts/release.sh check vX.Y.Z
#       The workflow's gate: the tag names mygo.json's version, CHANGELOG.md
#       has its section, mygo.json configures updates and main holds the
#       tagged commit.
#   scripts/release.sh notes X.Y.Z|Unreleased
#       A section of CHANGELOG.md, the release's notes.
#   scripts/release.sh publish X.Y.Z
#       Checks that the draft has every platform's downloads and update
#       manifest, its files against its SHA256SUMS and the manifests
#       against the version, publishes the release, which installed apps
#       then update to, then runs tap.
#   scripts/release.sh tap X.Y.Z
#       Fills packaging/homebrew/yt-music-player.rb with the published disk
#       image's SHA-256 and pushes it to the Homebrew tap: never an older
#       version, no commit when nothing changed. Rerun it when publish
#       published but failed to update the tap. With TAP_TOKEN, as in the
#       workflow, it clones and pushes with it rather than gh's login.
set -eu

cd "$(dirname "$0")/.."

die() {
	echo "release: $*" >&2
	exit 1
}

usage() {
	awk 'NR > 1 && /^#/ { sub(/^# ?/, ""); print; next } NR > 1 { exit }' "$0" >&2
	exit 2
}

need() {
	for tool; do
		command -v "$tool" >/dev/null || die "needs $tool"
	done
}

repo=xlanstar/yt-music-player
tap_repo=xlanstar/homebrew-tap
cask=yt-music-player

# The update targets of every release, each with its manifest.
platforms='darwin-universal linux-amd64 linux-arm64 windows-amd64 windows-arm64'

semver='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'

valid() { printf '%s\n' "$1" | grep -Eq "$semver"; }

# newer A B: A is a later version than B.
newer() {
	[ "$1" != "$2" ] &&
		[ "$(printf '%s\n%s\n' "$1" "$2" | sort -t . -k 1,1n -k 2,2n -k 3,3n | tail -n 1)" = "$1" ]
}

current() { jq -r .version mygo.json; }

# workdir sets tmp to a directory removed on exit.
workdir() {
	[ -z "${tmp-}" ] || return 0
	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT
}

# fetch X.Y.Z DIR downloads the release's disk image and SHA256SUMS into
# DIR, checks one against the other, and sets sha to the image's SHA-256.
# SHA256SUMS lists the update files too, which DIR has not.
fetch() {
	dmg=YT-Music-Player-$1-macos-universal.dmg
	gh release download "v$1" --repo "$repo" --dir "$2" --pattern "$dmg" --pattern SHA256SUMS
	sum=$(awk -v f="$dmg" '$2 == f' "$2/SHA256SUMS")
	[ -n "$sum" ] || die "SHA256SUMS does not list $dmg"
	(cd "$2" && printf '%s\n' "$sum" | shasum -a 256 -c >/dev/null) || die "$dmg does not match SHA256SUMS"
	sha=$(shasum -a 256 "$2/$dmg" | cut -d ' ' -f 1)
}

# downloads X.Y.Z prints the files people install from, one a line.
downloads() {
	echo "YT-Music-Player-$1-macos-universal.dmg"
	for arch in amd64 arm64; do
		echo "YT-Music-Player-$1-linux-$arch.deb"
		echo "YT-Music-Player-$1-windows-$arch-setup.exe"
	done
	echo install.sh
}

# feed X.Y.Z DIR PLATFORM checks the platform's update manifest among the
# release's files in DIR, which installed apps read once the release is the
# latest: it names X.Y.Z, and the archive and deltas it links to are files
# of this release.
feed() {
	name=update-$3.json
	manifest=$2/$name
	[ -f "$manifest" ] || die "v$1 has no $name"
	v=$(jq -r .version "$manifest")
	[ "$v" = "$1" ] || die "$name names $v, not $1"
	base=https://github.com/$repo/releases/download/v$1/
	# Path-escaped URLs: no spaces to split.
	for url in $(jq -r '.url, (.deltas // [])[].url' "$manifest"); do
		case $url in
		"$base"*) [ -f "$2/${url#"$base"}" ] || die "v$1 lacks ${url#"$base"}, which $name links to" ;;
		*) die "$name links outside v$1: $url" ;;
		esac
	done
}

# updates fails unless mygo.json configures updates: a release without them
# would leave its users on it.
updates() {
	jq -e '.updates.publicKey and .updates.github' mygo.json >/dev/null ||
		die "mygo.json has no updates.publicKey and updates.github (docs/release.md#updates)"
}

# draft X.Y.Z prints whether the release is a draft, nothing without one.
draft() { gh release view "v$1" --repo "$repo" --json isDraft --jq .isDraft 2>/dev/null || true; }

# notes prints a section without its heading and its surrounding blank lines.
notes() {
	awk -v h="## [$1]" '
		index($0, h) == 1 { found = 1; next }
		found && /^## / { exit }
		found && /^[[:space:]]*$/ { if (started) blank++; next }
		found { while (blank > 0) { print ""; blank-- } print; started = 1 }
	' CHANGELOG.md
}

check() {
	version=${1#v}
	{ [ "$1" = "v$version" ] && valid "$version"; } ||
		die "$1 is not a vX.Y.Z tag"
	[ "$(current)" = "$version" ] || die "$1 but mygo.json has $(current)"
	updates
	[ -n "$(notes "$version")" ] || die "CHANGELOG.md has no section for $version"
	git merge-base --is-ancestor "$1^{commit}" origin/main || die "$1 is not on main"
}

bump() {
	IFS=. read -r major minor patch <<EOF
$(current)
EOF
	case $1 in
	major) echo "$((major + 1)).0.0" ;;
	minor) echo "$major.$((minor + 1)).0" ;;
	patch) echo "$major.$minor.$((patch + 1))" ;;
	esac
}

release() {
	need git jq prek
	from=$(current)
	valid "$from" || die "mygo.json's version $from is not X.Y.Z"
	version=$(bump "$1")
	tag=v$version

	[ "$(git symbolic-ref --short -q HEAD)" = main ] || die "not on main"
	[ -z "$(git status --porcelain)" ] || die "the working tree has changes"
	git fetch --quiet --tags origin main
	[ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] ||
		die "main differs from origin/main: pull or push first"
	! git rev-parse -q --verify "refs/tags/$tag" >/dev/null || die "$tag exists"
	changes=$(notes Unreleased)
	[ -n "$changes" ] || die "CHANGELOG.md's Unreleased section is empty"
	updates

	printf '%s → %s\n\n%s\n\nRun the checks, commit, tag %s and push? [y/N] ' \
		"$from" "$version" "$changes" "$tag"
	read -r answer
	[ "$answer" = y ] || die "stopped, nothing changed"

	prek run --all-files

	awk -v v="$version" -v d="$(date +%Y-%m-%d)" '
		!done && $0 == "## [Unreleased]" { print; print ""; print "## [" v "] - " d; done = 1; next }
		{ print }
	' CHANGELOG.md >CHANGELOG.md.tmp
	mv CHANGELOG.md.tmp CHANGELOG.md
	jq --indent 2 --arg v "$version" '.version = $v' mygo.json >mygo.json.tmp
	mv mygo.json.tmp mygo.json
	git add mygo.json CHANGELOG.md
	if ! git commit --quiet -m "chore(release): $tag"; then
		git checkout HEAD -- mygo.json CHANGELOG.md
		die "the commit failed, mygo.json and CHANGELOG.md are restored"
	fi
	git tag -a "$tag" -m "$tag"
	git push --atomic origin main "$tag" ||
		die "the push failed; commit and tag are local: git push --atomic origin main $tag"

	echo "Pushed $tag. The Release workflow builds it into a draft release (gh run watch);"
	echo "check the draft, then approve the run's publish job, which publishes it."
}

publish() {
	need gh git jq shasum
	valid "$1" || die "$1 is not X.Y.Z"
	[ "$(draft "$1")" = true ] ||
		die "v$1 has no draft release: is the workflow's draft job done, and can gh's account write $repo (gh auth status)?"
	workdir
	mkdir "$tmp/draft"
	gh release download "v$1" --repo "$repo" --dir "$tmp/draft"
	(cd "$tmp/draft" && shasum -a 256 -c SHA256SUMS >/dev/null) || die "v$1's files do not match SHA256SUMS"
	# shasum checks only the files listed: a file left out goes unchecked.
	for f in "$tmp/draft"/*; do
		f=${f##*/}
		[ "$f" = SHA256SUMS ] || awk -v f="$f" '$2 == f { found = 1 } END { exit !found }' "$tmp/draft/SHA256SUMS" ||
			die "SHA256SUMS does not list $f"
	done
	for f in $(downloads "$1"); do
		[ -f "$tmp/draft/$f" ] || die "v$1 has no $f"
	done
	for platform in $platforms; do
		feed "$1" "$tmp/draft" "$platform"
	done
	gh release edit "v$1" --repo "$repo" --draft=false --latest >/dev/null
	echo "Published v$1. Should the tap fail now: scripts/release.sh tap $1"
	tap "$1"
}

tap() {
	need gh git shasum
	valid "$1" || die "$1 is not X.Y.Z"
	[ "$(draft "$1")" = false ] || die "v$1 is not published: the cask points only to published files"
	workdir
	# What users download, not what the draft held.
	mkdir "$tmp/published"
	fetch "$1" "$tmp/published"
	if [ -n "${TAP_TOKEN-}" ]; then
		# A header, as actions/checkout sets it, keeps the token out of URLs.
		auth="AUTHORIZATION: basic $(printf 'x-access-token:%s' "$TAP_TOKEN" | base64 | tr -d '\n')"
		git -c http.extraHeader="$auth" clone --quiet --depth 1 "https://github.com/$tap_repo.git" "$tmp/tap"
		git -C "$tmp/tap" config http.extraHeader "$auth"
	else
		gh repo clone "$tap_repo" "$tmp/tap" -- --quiet --depth 1
	fi
	file=$tmp/tap/Casks/$cask.rb
	if [ -f "$file" ]; then
		cur=$(sed -n 's/^  version "\(.*\)"$/\1/p' "$file")
		! newer "$cur" "$1" || die "the tap has $cur, newer than $1"
	fi
	mkdir -p "$tmp/tap/Casks"
	sed -e "s/@VERSION@/$1/g" -e "s/@SHA256@/$sha/g" "packaging/homebrew/$cask.rb" >"$file"
	! grep -n '@[A-Z0-9_]*@' "$file" || die "packaging/homebrew/$cask.rb has a placeholder left"
	# Cask rules apply to files in a Casks directory.
	if command -v brew >/dev/null; then
		brew style "$file" >/dev/null || die "brew style: fix packaging/homebrew/$cask.rb"
	else
		echo "release: no Homebrew, brew style skipped" >&2
	fi
	git -C "$tmp/tap" add Casks
	if git -C "$tmp/tap" diff --cached --quiet; then
		echo "The tap already has $cask $1."
		return
	fi
	git -C "$tmp/tap" commit --quiet -m "$cask $1"
	git -C "$tmp/tap" push --quiet origin HEAD
	echo "Updated $tap_repo: brew install --cask xlanstar/tap/$cask"
}

case ${1-} in
patch | minor | major)
	[ $# -eq 1 ] || usage
	release "$1"
	;;
check | notes | publish | tap)
	[ $# -eq 2 ] || usage
	"$1" "$2"
	;;
*) usage ;;
esac
