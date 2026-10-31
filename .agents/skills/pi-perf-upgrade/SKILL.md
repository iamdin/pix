---
name: pi-perf-upgrade
description: Move pitch's Pi performance patches to a new upstream Pi tag. Each patch (docs/pi-perf/<name>/) is one optimization method, applied and tested on its own; implements them on a branch from the tag, checks each one's tests, and gates every patch alone and against the full set with bench/run.ts on AOT builds, so nothing gets slower. Use when upgrading the Pi version pitch builds, or to re-check the patches on the current one.
---

# Upgrading Pi with the performance patches

Paths are relative to the pitch project (`/Volumes/SSD/GitHub/iamdin/pitch`). The Pi fork is `/Volumes/SSD/GitHub/iamdin/pi`
(remotes: `origin` = earendil-works/pi, `bolt` = opensec-git/Pi-Bolt, reference only).

## What a patch is

One optimization method (one dimension: "streaming Markdown renders only what changed", "highlighted code is kept"), not
one piece of code. Each is a directory `docs/pi-perf/<name>/`, named by its method, never numbered.

- `SPEC.md`: **the authority.** The method, what to change and why, the invariants, how to find the code, the tests, the
  performance gate, when to skip. Format: `docs/pi-perf/SPEC-TEMPLATE.md`.
- `<tag>.diff`: what implementing the spec produced on that Pi tag. A record and a reference for the next
  implementation, not the definition: code moves, and a diff that still applies can still be wrong.

Patches are independent in effect: each one alone on the tag makes nothing worse, and the full set without it is
measurably slower. Code may overlap, but the diffs must apply in any order and any subset; effects may not depend on
another patch. When one does, fix it in the patch or merge the two, never add a dependency.

## Inputs

- `TAG`: the upstream Pi tag to move to (only release tags, never main).
- `BUN`: the Bun pitch pins (`internal/pitch/data/sources.lock.json`); run every build and the bench with it. Bun AOT
  exists only in canary builds, so pitch follows official Bun canaries. When only the Bun pin changes, run steps 4–6
  (combos, comparison with the previous build, default build) on the unchanged patches: a new runtime can move the numbers.

## Steps

1. In the fork: `git fetch origin tag TAG`, `git switch -c perf-TAG TAG`, install dependencies.
2. For each `docs/pi-perf/<name>/SPEC.md`:
   1. Read the spec completely. If its "skip when" holds, record why in `docs/pi-perf/results/TAG/log.md`; go on.
   2. Implement it on the new code from the spec. The latest `<tag>.diff` may be tried with `git apply --3way` as a
      shortcut; either way, check the result against every invariant and step of the spec.
   3. Run the spec's tests. All must pass, including the ones it adds. Compare failures with the untouched tag first.
   4. Commit: `perf(<name>): <summary>`.
3. Write each `docs/pi-perf/<name>/TAG.diff` from its commits (`git diff <before> <after>`). Keep the older ones. Check
   that every subset applies on the tag in every order and all orders give the same tree:
   `bun bench/check-patch-orders.ts --repo /abs/pi --tag TAG` (temporary index, work tree untouched). When one does not,
   regenerate that diff with less context (`git diff -U1`) or change the code so the patches do not touch neighbouring
   lines; record it in the spec.
4. Gate on AOT, which is what pitch ships: `bun bench/build-variants.ts --tag TAG --out /abs/new-dir --combos` builds every
   subset (2^n builds of about half a minute each), then
   `bun bench/run.ts $(sed 's/^/--build /' builds.txt) --scenarios startup,write:200,tuiwrite:200,md:20000,md:60000
   --rounds 3 --warmup 1`. For each patch, both must hold (exit status 2 of a narrower `--expect`/`--max-regress` run means
   stop and find out why):
   - alone vs `base`: no scenario more than 5% worse in CPU, wall or RSS, and the gain its spec expects;
   - all vs all without it: the gain its spec expects. No gain: propose dropping it (to the user), do not just keep it.
   Look for interactions in the other subsets too: a pair that is worse than either alone is a bug in one of them.
5. Against the previous Pi version's final build: the full set may be at most 5% worse in any scenario.
6. Ship them as pitch's builtin patches: copy each `docs/pi-perf/<name>/TAG.diff` to `internal/pitch/data/<name>.patch`
   (remove patches dropped this round), update `features` in `internal/pitch/catalog.go` (SHA-256, note, the spec's test
   commands) and the Pi pin in `sources.lock.json`; `PITCH_TEST_PI=<pristine pinned Pi> go test ./...` (every subset
   applies); `go build -o bin/pitch ./cmd/pitch` and a default `bin/pitch build` (runs every patch's tests, smoke and
   the artifact hash check). Record everything under `docs/pi-perf/results/TAG/` and update the specs ("Verified on",
   numbers, what changed) and the README's numbers.

## Notes from v1.4.0

- `docs/pi-perf/results/v1.4.0/log.md`: how the patches were found and measured (it uses the old working numbers),
  the changes not taken and why, the write renderer memory bug, and the tests that already fail on the untouched tag.
- A JIT run (`BUN /abs/snapshot/packages/coding-agent/src/cli.ts`) is a cheap first look but disagreed with AOT for
  parts of streaming-markdown; report it apart, AOT decides.
- In zsh, `$name:r` in a refspec is a modifier and `for x in $list` does not split; use bash for loops.

## Rules

- One method per patch; one or more commits per patch, never one commit for two.
- Keep every result, also neutral and failed ones. Never overwrite an earlier results directory.
- Numbers from `bun cli.ts` (JIT) and from AOT binaries are reported apart.
