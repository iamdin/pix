---
name: pi-perf-upgrade
description: Move pitch's Pi performance patches to a new upstream Pi tag. Each patch (docs/pi-perf/<name>/) is one optimization method and one commit on pitch's Pi fork (github.com/getpie-dev/pi, branch pitch-<tag>); rebases the branch onto the tag, checks each patch against its spec and tests, and gates every patch alone and against the full set with bench/run.ts on AOT builds, so nothing gets slower. Use when upgrading the Pi version pitch builds, or to re-check the patches on the current one.
---

# Upgrading Pi with the performance patches

Paths are relative to the pitch project (`/Volumes/SSD/GitHub/iamdin/pitch`). The Pi fork is `/Volumes/SSD/GitHub/iamdin/pi`
(remotes: `getpie` = github.com/getpie-dev/pi, pitch's fork; `origin` = earendil-works/pi; `bolt` = opensec-git/Pi-Bolt,
reference only). pitch builds the fork's branch `pitch-<tag>`: the upstream tag plus one commit per patch, pinned by
commit in `internal/pitch/data/sources.lock.json`. There are no patch files.

## What a patch is

One optimization method (one dimension: "streaming Markdown renders only what changed", "highlighted code is kept"), not
one piece of code. Each is a directory `docs/pi-perf/<name>/`, named by its method, never numbered.

- `SPEC.md`: **the authority.** The method, what to change and why, the invariants, how to find the code, the tests, the
  performance gate, when to skip. Format: `docs/pi-perf/SPEC-TEMPLATE.md`.
- Its commit `perf(<name>): <summary>` on `pitch-<tag>`: what implementing the spec produced on that tag. A record
  and the start of the next implementation, not the definition: code moves, and a commit that rebases cleanly can still
  be wrong.

Patches are independent in effect: each one alone on the tag makes nothing worse, and the full set without it is
measurably slower. Code may overlap, but each commit must revert cleanly from the full set (that is how
`bench/build-variants.ts` builds subsets); effects may not depend on another patch. When one does, fix it in the patch or merge the two, never add a dependency.

## Inputs

- `TAG`: the upstream Pi tag to move to (only release tags, never main).
- `BUN`: the Bun pitch pins (`internal/pitch/data/sources.lock.json`); run every build and the bench with it. Bun AOT
  exists only in canary builds, so pitch follows official Bun canaries. When only the Bun pin changes, run steps 4–6
  (combos, comparison with the previous build, default build) on the unchanged patches: a new runtime can move the numbers.

## Steps

1. In the fork: `git fetch origin tag TAG`, `git switch -c pitch-TAG pitch-OLD`, `git rebase --onto TAG OLD` (OLD is the
   current tag). Git replays each patch commit with a three-way merge; resolve conflicts from the spec, not by taking
   either side blindly. Install dependencies.
2. For each `docs/pi-perf/<name>/SPEC.md`:
   1. Read the spec completely. If its "skip when" holds, drop its commit, record why in
      `docs/pi-perf/results/TAG/log.md`; go on.
   2. Check the rebased commit against every invariant and step of the spec; fix it in that commit (`git commit
      --fixup` and an autosquash rebase), keeping one method per commit.
   3. Run the spec's tests. All must pass, including the ones it adds. Compare failures with the untouched tag first.
3. `git push getpie pitch-TAG` (as gh account oxwen11, an admin of getpie-dev). In pitch: set `pi` in `internal/pitch/data/sources.lock.json` (tag, branch, commit; the
   model catalog revision from the tag's `nix/model-catalog.json`), update `piTests` in `internal/pitch/catalog.go` to
   the specs' tests, `go test ./...`, `go build -o bin/pitch ./cmd/pitch`.
4. Gate on AOT, which is what pitch ships: `bun bench/build-variants.ts --repo /abs/pi --tag TAG --branch pitch-TAG
   --out /abs/new-dir --combos` builds every subset (2^n builds of a few minutes each), then
   `bun bench/run.ts $(sed 's/^/--build /' builds.txt) --scenarios startup,write:200,tuiwrite:200,md:20000,md:60000
   --rounds 3 --warmup 1`. For each patch, both must hold (exit status 2 of a narrower `--expect`/`--max-regress` run means
   stop and find out why):
   - alone vs `base`: no scenario more than 5% worse in CPU, wall or RSS, and the gain its spec expects;
   - all vs all without it: the gain its spec expects. No gain: propose dropping it (to the user), do not just keep it.
   Look for interactions in the other subsets too: a pair that is worse than either alone is a bug in one of them.
5. Against the previous Pi version's final build: the full set may be at most 5% worse in any scenario.
6. A development `bin/pitch build --out /abs/dir` prepares Pi locally (every patch's tests, smoke, the artifact hash
   check); `bun bench/e2e/run.ts /abs/dir/pi`. Record everything under `docs/pi-perf/results/TAG/` and update the specs
   ("Verified on" with the new commits, numbers, what changed) and the README's numbers. `scripts/release.ts` prepares
   the Pi a release embeds.

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
