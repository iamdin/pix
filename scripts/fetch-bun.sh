#!/bin/sh
# Puts the pinned Bun canary archives into internal/pix/runtime-data/<goos>-<goarch>/bun.zip, from their URLs in
# sources.lock.json, checked against its sums. Bun deletes old canary downloads: when they are gone, pin a newer canary
# (.agents/skills/pi-perf-upgrade/SKILL.md). Releases have Bun inside pix, so users never download it. Needs gh, jq.
set -eu
cd "$(dirname "$0")/.."
lock=internal/pix/data/sources.lock.json
for p in $(jq -r '.bun.platforms | keys[]' "$lock"); do
	zip=internal/pix/runtime-data/$(echo "$p" | tr / -)/bun.zip
	url=$(jq -r --arg p "$p" '.bun.platforms[$p].url' "$lock")
	gh api -H "Accept: application/octet-stream" "${url#https://api.github.com/}" > "$zip" # signed in: no rate limit
	echo "$(jq -r --arg p "$p" '.bun.platforms[$p].sha256' "$lock")  $zip" | shasum -a 256 -c -
done
