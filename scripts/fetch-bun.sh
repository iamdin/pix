#!/bin/sh
# Puts the pinned Bun canary archives into internal/pix/runtime-data/<goos>-<goarch>/bun.zip, from their URLs in
# sources.lock.json (pix's copy, scripts/mirror-bun.sh), checked against its sums. Releases have Bun inside pix, so
# users never download it. Needs jq.
set -eu
cd "$(dirname "$0")/.."
lock=internal/pix/data/sources.lock.json
for p in $(jq -r '.bun.platforms | keys[]' "$lock"); do
	zip=internal/pix/runtime-data/$(echo "$p" | tr / -)/bun.zip
	url=$(jq -r --arg p "$p" '.bun.platforms[$p].url' "$lock")
	curl -fsSL "$url" -o "$zip"
	echo "$(jq -r --arg p "$p" '.bun.platforms[$p].sha256' "$lock")  $zip" | shasum -a 256 -c -
done
