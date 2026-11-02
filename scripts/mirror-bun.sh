#!/bin/sh
# Keeps the pinned Bun canary archives (internal/pix/runtime-data/<goos>-<goarch>/bun.zip) in a pre-release of
# iamdin/pix, bun-<revision>, and points sources.lock.json at it: Bun replaces its canary downloads with every new
# canary, so a release could not download the pinned one for long. Run once after pinning a canary. Needs gh, jq.
set -eu
cd "$(dirname "$0")/.."
lock=internal/pix/data/sources.lock.json
tag=bun-$(jq -r .bun.revision "$lock" | tr + -)
files=
for p in $(jq -r '.bun.platforms | keys[]' "$lock"); do
	n=$(echo "$p" | tr / -)
	zip=internal/pix/runtime-data/$n/bun.zip
	echo "$(jq -r --arg p "$p" '.bun.platforms[$p].sha256' "$lock")  $zip" | shasum -a 256 -c -
	cp "$zip" "${TMPDIR:-/tmp}/bun-$n.zip"
	files="$files ${TMPDIR:-/tmp}/bun-$n.zip"
	jq --arg p "$p" --arg url "https://github.com/iamdin/pix/releases/download/$tag/bun-$n.zip" '.bun.platforms[$p].url = $url' "$lock" > "$lock.new"
	mv "$lock.new" "$lock"
done
# A pre-release, so that it is never pix's latest release.
gh release create "$tag" -R iamdin/pix --prerelease --latest=false --title "$tag" \
	--notes "Bun $(jq -r .bun.revision "$lock") ($(jq -r .bun.commit "$lock")), as downloaded from oven-sh/bun; pix's releases build with it. Not a pix release." $files
