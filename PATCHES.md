# Patches

## pitch's patches

The Pi that pitch builds is a fork; there are no patch files in this repository. Fork rules:

- Repository: [getpie-dev/pi](https://github.com/getpie-dev/pi), forked from earendil-works/pi. The patches are
  maintained only there; they are not proposed upstream.
- Branches: one per upstream release tag, named `pitch-<tag>`, for example `pitch-v1.4.0`. A branch is the upstream tag
  plus one commit per patch, `perf(<name>): <summary>`, matching `docs/pi-perf/<name>/`.
- Pinning: `internal/pitch/data/sources.lock.json` pins a commit; `branch` must be `pitch-` and `tag` (`TestPins`
  checks it). A release requires the pinned commit to be the head of the branch.
- Append only: once a pitch release pins a branch, the branch is never deleted or force-pushed, or older pitch releases
  could not fetch their commit. To fix a patch after a release, add a `fix(<name>): ...` commit to the branch; fold it
  into its `perf` commit when the next Pi upgrade makes a new branch.
- Pushing: as gh account oxwen11 (an admin of getpie-dev).

Each patch is one optimization method, described in `docs/pi-perf/<name>/SPEC.md`: method, invariants, tests,
performance gates and when to give it up. The spec is the authority; the commit is its implementation on one tag. When
Pi releases a new version, rebase the previous branch into `pitch-<new tag>` and check each patch against its spec,
tests and performance gates; see `.agents/skills/pi-perf-upgrade/SKILL.md`.

A release (`bun scripts/release.ts`) prepares Pi once, on the maintainer's machine: it fetches the pinned commit, runs
`npm ci`, builds, runs every patch's tests and embeds the result in pitch. Users' `pitch build` only compiles.

## Provenance

`sources.pi` in `pitch-build.json` records the Pi that was built: fork, branch and commit. `source_tree` is the Git tree
of that commit, and `checks` lists the checks that actually ran (the patch tests run only when the pinned commit is
prepared). Generated binaries are not promised to be byte-for-byte identical.

## Changing a patch

Change the commit in the fork, build Pi from it with `pitch prepare --repo /abs/pi --commit SHA /abs/pi.tar.gz` (needs
Git, Node 24+ and npm 11+) and compile with `pitch build --pi /abs/pi.tar.gz --out NEW`. To compare sets of patches, use
`bench/build-variants.ts`: it reverts the unwanted patches in the fork and takes the same two steps.
