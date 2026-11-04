---
name: pi-perf-upgrade
description: Move pix's Pi performance patches to a new upstream Pi tag. Each patch (docs/pi-perf/<name>/) is one optimization method and one commit on pix's Pi fork (github.com/getpie-dev/pi, branch pix-<tag>); rebases the branch onto the tag, checks each patch against its spec and tests, and gates every patch alone and against the full set with bench/run.ts on AOT builds, so nothing gets slower. Use when upgrading the Pi version pix builds, or to re-check the patches on the current one.
---

# Upgrading Pi with the performance patches

Paths are relative to the pix project (`/Volumes/SSD/GitHub/iamdin/pix`). The Pi fork is `/Volumes/SSD/GitHub/iamdin/pi`
(remotes: `getpie` = github.com/getpie-dev/pi, pix's fork; `origin` = earendil-works/pi). pix builds the
fork's branch `pix-<tag>`: the upstream tag plus one commit per patch, pinned by commit in
`internal/pix/data/sources.lock.json`. There are no patch files. The fork rules (naming, never rewriting or deleting
a branch a release pinned, `fix(<name>)` commits after release) are in PATCHES.md.

## What a patch is

One optimization method (one dimension: "streaming Markdown renders only what changed", "highlighted code is kept"), not
one piece of code. Each is a directory `docs/pi-perf/<name>/`, named by its method, never numbered.

- `SPEC.md`: **the authority.** The method, what to change and why, the invariants, how to find the code, the tests, the
  performance gate, when to skip. Format: `docs/pi-perf/SPEC-TEMPLATE.md`.
- Its commit `perf(<name>): <summary>` on `pix-<tag>`: what implementing the spec produced on that tag. A record
  and the start of the next implementation, not the definition: code moves, and a commit that rebases cleanly can still
  be wrong.

Patches are independent in effect: each one alone on the tag makes nothing worse, and the full set without it is
measurably slower. Code may overlap, but each commit must revert cleanly from the full set (that is how
`bench/build-variants.ts` builds subsets); effects may not depend on another patch. When one does, fix it in the patch or merge the two, never add a dependency.

## Inputs

- `TAG`: the upstream Pi tag to move to (only release tags, never main).
- `BUN`: the Bun pix pins (`internal/pix/data/sources.lock.json`); run every build and the bench with it. Bun AOT
  exists only in canary builds, so pix follows official Bun canaries. When only the Bun pin changes, the patches are
  unchanged: run step 5 (all patches vs the previous build), `base` vs `all` from step 4, and step 6. Only when one of
  them is worse or a gain is gone, run the combos of step 4 to find which patch.
  To pin a canary: download `bun-darwin-aarch64.zip`, `bun-linux-x64.zip` and `bun-linux-aarch64.zip` of one canary
  (all three `bun --revision` equal) into `internal/pix/runtime-data/<goos>-<goarch>/bun.zip`, and record each
  zip's and its binary's SHA-256 under `bun.platforms` in `sources.lock.json`, then run `sh scripts/mirror-bun.sh` (the user
  runs it: it publishes): it keeps the zips in a pre-release of pix and sets their URLs, as Bun replaces its canary downloads with every canary; WebKit is `WEBKIT_VERSION` in Bun's
  `scripts/build/deps/webkit.ts` at that commit.

## Steps

1. In the fork: `git fetch origin tag TAG`, `git switch -c pix-TAG pix-OLD`, `git rebase --onto TAG OLD` (OLD is the
   current tag). Git replays each patch commit with a three-way merge; resolve conflicts from the spec, not by taking
   either side blindly. Install dependencies.
2. For each `docs/pi-perf/<name>/SPEC.md`:
   1. Read the spec completely. If its "skip when" holds, drop its commit, record why in
      `docs/pi-perf/results/TAG/log.md`; go on.
   2. Check the rebased commit against every invariant and step of the spec; fix it in that commit (`git commit
      --fixup` and an autosquash rebase), keeping one method per commit.
   3. Run the spec's tests. All must pass, including the ones it adds. Compare failures with the untouched tag first.
3. `git push getpie pix-TAG` (as gh account oxwen11, an admin of getpie-dev). In pix: set `pi` in `internal/pix/data/sources.lock.json` (tag, branch, commit; the
   model catalog revision from the tag's `nix/model-catalog.json`), update `piTests` in `internal/pix/catalog.go` to
   the specs' tests, `go test ./...`, `go build -o bin/pix ./cmd/pix`.
4. Gate on AOT, which is what pix ships: `bun bench/build-variants.ts --repo /abs/pi --tag TAG --branch pix-TAG
   --out /abs/new-dir --combos` builds every subset (2^n builds of a few minutes each), then
   `bun bench/run.ts $(sed 's/^/--build /' builds.txt) --scenarios startup,write:200,tuiwrite:200,md:20000,md:60000
   --rounds 3 --warmup 1`. For each patch, both must hold (exit status 2 of a narrower `--expect`/`--max-regress` run means
   stop and find out why):
   - alone vs `base`: no scenario more than 5% worse in CPU, wall or RSS, and the gain its spec expects;
   - all vs all without it: the gain its spec expects, and more than 3% in the scenario it targets. 3% or less: drop it
     (move its spec to `docs/pi-perf/retired/`), a patch that small is not worth carrying.
   Look for interactions in the other subsets too: a pair that is worse than either alone is a bug in one of them.
5. Against the previous Pi version's final build: the full set may be at most 5% worse in any scenario.
6. `bin/pix self prepare /abs/pi.tar.gz` (every patch's tests), `bin/pix compile --pi /abs/pi.tar.gz --out /abs/dir` (smoke,
   the artifact hash check); `bun bench/e2e/run.ts /abs/dir/pi`. Record everything under `docs/pi-perf/results/TAG/` and update the specs
   ("Verified on" with the new commits, numbers, what changed) and the README's numbers. The release workflow prepares
   the Pi a release ships; pushing the commit to main releases it.

## Notes from v1.5.0

- Start with stock TAG vs the previous release's build (both AOT, same Bun): it shows what upstream already fixed and
  where the gap is. v1.5.0 made three patches unnecessary and exposed a cost one dropped patch had removed (the Mermaid
  lex): check a spec's skip condition step by step, not as a whole.
- `bin/pix` prepares only the Pi whose model catalog its `sources.lock.json` pins: build the previous version's variants
  before switching the pin (or keep a second binary built with the old lock).
- md scenarios are noisy on a loaded machine; rerun a gate that is near its threshold with 5 rounds, and the short
  scenarios (startup, write, tuiwrite) with 15.
- `docs/pi-perf/results/v1.5.0/log.md` records the decisions.

## Notes from v1.4.0

- `docs/pi-perf/results/v1.4.0/log.md`: how the patches were found and measured (it uses the old working numbers),
  the changes not taken and why, the write renderer memory bug, and the tests that already fail on the untouched tag.
- A JIT run (`BUN /abs/snapshot/packages/coding-agent/src/cli.ts`) is a cheap first look but disagreed with AOT for
  parts of streaming-markdown; report it apart, AOT decides.
- In zsh, `$name:r` in a refspec is a modifier and `for x in $list` does not split; use bash for loops.

## Rules

- One method per patch, one commit per patch.
- Keep every result, also neutral and failed ones. Never overwrite an earlier results directory.
- Numbers from `bun cli.ts` (JIT) and from AOT binaries are reported apart.
