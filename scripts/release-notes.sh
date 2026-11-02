#!/bin/sh
# Prints the notes of release TAG: how to install and update, what it is made of (sources.lock.json) and what changed
# since the previous release PREV, if any. Needs git history back to PREV and jq.
set -eu
tag=$1 prev=${2:-}
lock=internal/pix/data/sources.lock.json
pins() { # the pinned Pi and Bun at a git revision
	git show "$1:$lock" | jq -r '"Pi \(.pi.tag) with pix'"'"'s patches: \(.pi.repository | sub("\\.git$"; "")) \(.pi.branch) \(.pi.commit[0:9])", "Bun \(.bun.revision)"'
}
echo 'Install: `curl -fsSL https://github.com/iamdin/pix/releases/latest/download/install.sh | sh` or `npm i -g usepix`. Update: `pix update`.'
echo
pins "$tag" | sed 's/^/- /'
[ -n "$prev" ] || exit 0
echo
pins "$prev" > "${TMPDIR:-/tmp}/pins-prev.$$"
changed=$(pins "$tag" | paste -d '\n' "${TMPDIR:-/tmp}/pins-prev.$$" - | while read -r old && read -r new; do
	[ "$old" = "$new" ] || echo "- Changed since $prev: $old → ${new#* }"
done)
rm -f "${TMPDIR:-/tmp}/pins-prev.$$"
echo "${changed:-Pi and Bun as in $prev.}"
echo
echo "Commits since $prev:"
git log --format='- %s (%h)' "$prev..$tag"
