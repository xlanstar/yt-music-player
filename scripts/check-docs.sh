#!/usr/bin/env bash
# Checks the docs' links (AGENTS.md, Conventions): every relative link and
# anchor in the Markdown, every docs/<name>.md#anchor that code cites, page
# names in kebab-case, docs/ never linking to roadmap/, and AGENTS.md
# listing every page of docs/ and roadmap/ and every package of internal/.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

status=0
fail() {
	echo "$1" >&2
	status=1
}

# anchors prints a Markdown file's heading anchors, as GitHub makes them.
anchors() {
	awk '
		/^```/ { fence = !fence; next }
		fence || !/^#+ / { next }
		{
			h = tolower($0)
			sub(/^#+ +/, "", h)
			gsub(/[^a-z0-9 _-]/, "", h)
			gsub(/ /, "-", h)
			if (h in seen) { print h "-" seen[h]; seen[h]++ } else { print h; seen[h] = 1 }
		}
	' "$1"
}

# target checks a reference, path[#anchor] from the root, cited at where.
target() {
	local where=$1 ref=$2 file=${2%%#*} anchor=
	case $ref in *#*) anchor=${ref#*#} ;; esac
	if [ ! -e "$file" ]; then
		fail "$where: $ref: no such file"
	elif [ -n "$anchor" ] && ! anchors "$file" | grep -qxF -- "$anchor"; then
		fail "$where: $ref: no such heading"
	fi
}

# join resolves a relative path from a directory, both from the root; it
# fails on one that leaves the repository (GitHub's ../../releases).
join() {
	local path=$1/$2 out= part
	local IFS=/
	for part in $path; do
		case $part in
		'' | .) ;;
		..)
			[ -n "$out" ] || return 1
			out=${out%/*}
			;;
		*) out=$out/$part ;;
		esac
	done
	echo "${out#/}"
}

files() { git ls-files --cached --others --exclude-standard -- "$@"; }

for f in $(files 'docs/*.md' 'roadmap/*.md'); do
	basename "$f" | grep -qE '^[a-z0-9]+(-[a-z0-9]+)*\.md$' || fail "$f: name not in kebab-case"
	grep -qF -- "\`$f\`" AGENTS.md || fail "AGENTS.md: $f missing from Documentation"
done

for pkg in $(files 'internal/*.go' ':!:**/testdata/**' | xargs -n1 dirname | sort -u); do
	grep -qF -- "\`${pkg##*/}\`" AGENTS.md || fail "AGENTS.md: $pkg missing from Architecture"
done

# Relative links in Markdown outside code fences: [text](path#anchor).
for f in $(files '*.md' ':!:.agents/' ':!:**/testdata/**'); do
	dir=$(dirname "$f")
	while IFS=: read -r line link; do
		case $link in http://* | https://* | mailto:*) continue ;; esac
		ref=$f
		if [ "${link%%#*}" ]; then
			ref=$(join "$dir" "${link%%#*}") || continue
		fi
		case $link in *#*) ref=$ref#${link#*#} ;; esac
		case $f:$ref in docs/*:roadmap/*) fail "$f:$line: docs/ links to roadmap/" ;; esac
		target "$f:$line" "$ref"
	done < <(awk '
		/^```/ { fence = !fence; next }
		fence { next }
		{
			s = $0
			while (match(s, /\]\([^) ]+\)/)) {
				print FNR ":" substr(s, RSTART + 2, RLENGTH - 3)
				s = substr(s, RSTART + RLENGTH)
			}
		}
	' "$f")
done

# docs/<name>.md[#anchor] as code, scripts and AGENTS.md cite it; not in URLs.
while IFS=: read -r f line ref; do
	target "$f:$line" "${ref#?}"
done < <(git grep -nIoE '(^|[^/[:alnum:].])docs/[a-z0-9-]+\.md(#[a-z0-9_-]+)?' -- \
	':!:docs/' ':!:roadmap/' ':!:CHANGELOG.md' ':!:.agents/' | sed -E 's/^([^:]+:[0-9]+:)(docs\/)/\1 \2/')

exit "$status"
