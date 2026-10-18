#!/usr/bin/env bash
# Fails on a long Go comment or docs/ paragraph that the change touches
# (lines added since HEAD, or a new file), so older prose passes until edited.
set -euo pipefail

max_lines=4  # a Go comment block's lines, package docs aside
max_words=60 # a docs/ paragraph's words, one line each

status=0
for f in "$@"; do
	[ -f "$f" ] || continue
	if git cat-file -e "HEAD:$f" 2>/dev/null; then
		added=$(git diff -U0 HEAD -- "$f" | awk '/^@@/ {
			split(substr($3, 2), a, ",")
			c = (a[2] == "") ? 1 : a[2]
			for (i = 0; i < c; i++) printf "%d ", a[1] + i
		}')
	else
		added=all
	fi
	[ -n "$added" ] || continue

	case $f in
	*.go)
		awk -v f="$f" -v max="$max_lines" -v added="$added" '
			BEGIN { split(added, a, " "); for (i in a) add[a[i]] = 1 }
			function flush(after) {
				if (n > max && touched && after !~ /^package /) {
					printf "%s:%d: comment of %d lines (max %d)\n", f, start, n, max
					bad = 1
				}
				n = 0; touched = 0
			}
			/^[ \t]*\/\/(go:|nolint|line )/ { flush($0); next }
			/^[ \t]*\/\// {
				if (!n) start = FNR
				n++
				if (added == "all" || FNR in add) touched = 1
				next
			}
			{ flush($0) }
			END { flush(""); exit bad }
		' "$f" || status=1
		;;
	*.md)
		awk -v f="$f" -v max="$max_words" -v added="$added" '
			BEGIN { split(added, a, " "); for (i in a) add[a[i]] = 1 }
			/^```/ { fence = !fence; next }
			fence || /^\|/ { next }
			(added == "all" || FNR in add) && NF > max {
				printf "%s:%d: paragraph of %d words (max %d)\n", f, FNR, NF, max
				bad = 1
			}
			END { exit bad }
		' "$f" || status=1
		;;
	esac
done

if [ "$status" -ne 0 ]; then
	echo "Cut to the deciding why; measurements and history go in the commit body." >&2
fi
exit "$status"
