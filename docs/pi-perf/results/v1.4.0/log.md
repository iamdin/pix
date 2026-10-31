# v1.4.0 log

Machine: Apple M4, 24 GiB, not pinned to cores. Bun 1.4.4-canary.1+000ef5037 (pitch's pin).
JIT runs: `bun packages/coding-agent/src/cli.ts` from snapshots `pitch/.work/pi-perf-snapshots/NNN`
(baseline v1.4.0: `pitch/.work/pi-opt-20261030/pi`). AOT runs: `pitch build` outputs in `pitch/.work/pi-perf-aot/`.

## Per spec (JIT unless noted)

| spec | fork commit | gate | headline |
|---|---|---|---|
| 001 | 9e72b0f2e | pass | md:20000 CPU 8.32 → 5.47 s |
| 002 | 1dd21fff1 | pass | md:20000 CPU 5.45 → 3.16 s |
| 003 | ec5c38e15 | pass | write:200 CPU 27.66 → 0.62 s |
| 004 | 372b63204 | pass | tuiwrite:400 CPU 1.05 → 0.82 s |
| 005 | 354078c9b | pass (max-regress only) | md:60000 RSS 245.6 → 221.9 MB |
| R1 | 1c9857708, reverted by aa4b56cf0 | **fail** | Pi-Bolt f2fae008c as a whole: md:20000 CPU +2.3% (no gain), write:50 wall +7.0% (0.209 → 0.224 s, ranges apart; CPU equal). Cause not investigated. Its assistant-message part was later taken into 010. |
| 006–010 | c324c8ac7 … 009f7a05f | pass on AOT (group) | JIT (`006-010.txt`): md:20000 CPU 3.09 → 2.88 s (−6.8%), tuiwrite:200 CPU 0.54 → 0.61 s (+13%, ranges apart). AOT (`aot.txt`): md:20000 1.78 → 1.54 s (−13.5%), tuiwrite equal. |

006–010 were gated as a group (chain run `006-010.txt`) instead of one gate each.

## Whole set on AOT (`aot.txt`, 3 rounds + 1 warm-up, medians)

| scenario | v1.4.0 | 001–005 | 001–010 |
|---|---|---|---|
| startup CPU | 27 ms | 22 ms | 18 ms (noisy) |
| write:200 (`pi -p`) CPU / wall | 9.56 s / 9.54 s | 0.096 s / 0.098 s | 0.095 s / 0.097 s |
| tuiwrite:200 CPU / wall | 15.43 s / 15.50 s | 0.18 s / 0.31 s | 0.18 s / 0.31 s |
| md:20000 CPU | 6.41 s | 1.78 s | 1.54 s |
| md:20000 RSS after | 152.6 MB | 93.1 MB | 92.0 MB |

The 10 per-spec diffs passed to `pitch build --patch` in order give the same `source_tree`
(36e280e066ec532636cc72b678419deaa9048ad1) as the cumulative diff; `pitch verify` passes.

## Tests

`packages/tui` all pass. `packages/coding-agent` 2821 passed, 3 failed; `packages/ai` 1447 passed, 7 failed;
`packages/agent` 92 passed. The 10 failures fail identically on untouched v1.4.0 (environment/network-dependent):
model-runtime-classifiers (1), package-command-paths (2), classifier-models (1), cloudflare-workers-ai-system-one (6).

## Ablation on AOT (`ablation-aot.txt`, 3 rounds + 1 warm-up, medians)

Builds (`bench/build-variants.ts`, in `pitch/.work/pi-perf-ablation/`): `cNNN` = specs 001..NNN; `no003`, `no004` = all but
that one. Marginal effect = cNNN vs c(NNN-1), or all vs noNNN.

| spec | marginal effect | verdict |
|---|---|---|
| 001 | md:20000 CPU 6.25 → 3.73 s (−40%), RSS 139.6 → 123.0 MB | big |
| 002 | md:20000 CPU 3.73 → 1.74 s, RSS 123 → 97 MB; tuiwrite:200 CPU 15.5 → 10.3 s | big |
| 003 | write:200 CPU 9.6 → 0.096 s; tuiwrite:200 10.3 → 0.27 s (all vs no003: 10.2 → 0.18 s) | big |
| 004 | tuiwrite:200 CPU 0.27 → 0.19 s, RSS 129 → 105 MB (all vs no004: 0.25 → 0.18 s, 130 → 105 MB), ranges apart | medium (small in absolute terms) |
| 005 | md:20000 RSS 94.3 → 89.4 MB, not held in c006–c010 (92–93 MB) | no clear gain |
| 006 | none | no clear gain (prerequisite of 007) |
| 007 | md:20000 CPU 1.74 → 1.54 s (−11%), ranges apart | medium |
| 008, 009, 010 | md:20000 CPU 1.54 → 1.52 → 1.49 → 1.51 s | no clear gain at 20000 chars |

Startup: all variants 15–17 ms CPU; base's median (29 ms) is noise. md:20000 wall is pacing (≈17.3 s) for every build.

Found by the ablation: **c002 and no003 use 3.7–4.4 GB RSS in tuiwrite:200** (base 292 MB, c001 298 MB). 002 without
003 makes the growing write call much more memory-hungry. With 003 (the shipped set) RSS is 105 MB. Cause not
investigated; 002 should not be shipped without 003.

## Confirmation on AOT (`confirm-aot.txt`, 7 rounds + 1 warm-up, medians)

| build | md:20000 CPU | md:60000 CPU | md:60000 RSS |
|---|---|---|---|
| c005 | 1.71 s | 6.29 s | 127.6 MB |
| c006 | 1.74 s | 6.52 s (+3.7%, ranges overlap) | 130.3 MB |
| c007 | 1.54 s (−9.9%) | 4.98 s (−20.8%, ranges apart) | 126.4 MB |
| c010 | 1.50 s | 4.42 s (−11% vs c007, ranges apart) | **173.6 MB (+37% vs c007, ranges apart)** |

- 007 confirmed; its gain grows with the reply length.
- 006 alone: no gain.
- 008–010: −11% CPU at 60000 chars, but +47 MB RSS. That fails the 5% gate.

Which of 008–010 costs the memory (`confirm-008-010.txt`, md:60000, 3 rounds + 1 warm-up):

| build | CPU | RSS |
|---|---|---|
| c007 | 5.00 s | 130.2 MB |
| c008 | 4.90 s (−2%) | 125.4 MB |
| c009 | 4.79 s (−2% more, ranges overlap) | 120.9 MB |
| c010 | 4.45 s (−7% more) | **174.2 MB (+53 MB vs c009, ranges apart)** |

The extra memory comes from 010 alone. 008 and 009 give small gains, with no extra validation (small changes).

## Why 002 without 003 uses GBs (JIT, `bun cli.ts` on snapshot copies, tuiwrite:200, 1 round each)

| variant (on snapshot 002) | CPU | RSS |
|---|---|---|
| snapshot 001 | 29.8 s | 506 MB |
| 002 | 13.2 s | 3114 MB |
| 002, cache off (`rememberHighlightedCode` returns at once) | 28.3 s | 500 MB |
| 002, key copied (JSON round-trip) | 12.9 s | 3092 MB |
| 002, `Bun.gc(true)` every 2000 calls | 13.5 s | 2827 MB (still reachable, not GC lag) |
| 002, `/\0/.test(code)` first in `highlightedLines` | 25.6 s | 533 MB |
| 002, write renderer copies its delta (below) | 24.9 s | 610 MB |

Heap snapshot (`Bun.generateHeapSnapshot('v8')`) mid-run: strings 161 MB. The big ones are 70 KB sliced strings, kept
by the ropes in `WriteCallRenderComponent.cache.normalizedLines`.

Cause: the write call renderer (upstream `core/tools/renderers/write.ts`) builds each line from
`fileContent.slice(previousLength)`. That slice shares the whole `fileContent` string, which is a new string on every
chunk when the arguments are parsed in full (no 003). Upstream, highlight.js reads every line through a regex, which
flattens the rope and drops those references as a side effect. 002 answers from its cache without that read, so every
line keeps a whole earlier copy of the file.

Fix, to go with 002: copy the delta in `updateWriteHighlightCacheIncremental`. 002's tuiwrite gain without 003 was mostly
this skipped flattening; with the fix it is 28.8 → 24.9 s (JIT, 1 round).

## Regrouped by method: A–D (fork `perf2-v1.4.0`)

A = chain 001, 005–009 (streaming Markdown); B = 002 + a copy in the write renderer (highlight cache); C = 003 (tool
arguments); D = 004 (collapsed write call). 010 dropped. Specs in `docs/pi-perf/{a,b,c,d}-*/`, the chain in
`docs/pi-perf/v1.4.0-chain/`.

### Every subset on AOT (`combos-aot.txt`, 16 builds, 3 rounds + 1 warm-up, medians)

| build | write:200 CPU | tuiwrite:200 CPU / RSS | md:20000 CPU / RSS | md:60000 CPU / RSS |
|---|---|---|---|---|
| base | 9.66 s | 15.53 s / 319 MB | 6.55 s / 144 MB | 24.19 s / 172 MB |
| a | 9.67 | 15.51 / 301 | 3.57 / 123 | 16.13 / 163 |
| b | 9.64 | 9.95 / 329 | 5.58 / 141 | 21.05 / 166 |
| c | 0.098 | 0.31 / 124 | 6.42 / 147 | 24.26 / 168 |
| d | 9.68 | 15.78 / **2770** | 6.43 / 140 | 24.25 / 175 |
| abcd | 0.098 | 0.18 / 105 | 1.48 / 90 | 4.82 / 118 |
| bcd (no A) | 0.097 | 0.19 / 106 | 5.34 / 134 | 21.37 / 164 |
| acd (no B) | 0.098 | 0.22 / 106 | 3.57 / 119 | 16.54 / 161 |
| abd (no C) | 9.70 | 10.27 / **2112** | 1.52 / 91 | 4.79 / 124 |
| abc (no D) | 0.097 | 0.26 / 124 | 1.49 / 93 | 4.88 / 132 |

| patch | alone vs base | all vs all without it | verdict |
|---|---|---|---|
| A | md:20000 −45%, md:60000 −33%, RSS lower; nothing worse | md:20000 −72%, md:60000 −77% | keep |
| B | md −15%/−13%, tuiwrite CPU −36%; tuiwrite RSS +3% (B's write-renderer copy works on AOT) | md:20000 −59%, md:60000 −71% | keep |
| C | write/tuiwrite −99%, tuiwrite RSS −61% | write −99% | keep |
| D | **tuiwrite RSS 319 → 2770 MB: fails** | tuiwrite CPU −31%, wall −20%, RSS −15% | fix, then keep |

A and B together give much more than the sum of each alone (md:60000: A 16.1 s, B 21.1 s, AB 4.8 s): A's per-block
cache needs B's stable styles and cheap code blocks. That is a gain, not a dependency: neither is worse alone.

Startup: every build 16–19 ms CPU median except abcd, 27 ms (range 16–38 ms, as wide as most builds'); re-measured below.

D's failure has the same cause as B's write renderer copy: D's collapsed path builds its lines from
`fileContent.slice()` too and no longer highlights them (which flattened them). Fixed in D (fork 331f79e3a, copy the
delta in the collapsed path); D's diff regenerated (-U1), all 15 subsets apply, A–D tree equals the fork HEAD.

### D fixed (`combos-d2-aot.txt`, 3 rounds + 1 warm-up, medians; `d`, `abcd` = before the fix)

| build | tuiwrite:200 CPU / RSS | md:20000 CPU / RSS | startup CPU |
|---|---|---|---|
| base | 15.53 s / 316 MB | 6.38 s / 141 MB | 28 ms (17–30) |
| d | 15.66 / 3567 | 6.34 / 149 | 19 ms |
| d2 | 15.48 / 299 | 6.36 / 148 | 17 ms |
| abd2 | 9.90 / 325 | 1.49 / 91 | 17 ms |
| abcd | 0.19 / 105 | 1.50 / 92 | 17 ms |
| abcd2 | 0.18 / 105 | 1.49 / 92 | 17 ms |

D alone no longer costs memory (299 vs 316 MB); A–D unchanged by the fix. md:20000 RSS of d/d2 (+5%, ranges overlap
base's) is noise: D does not touch Markdown. Startup: abcd's 27 ms in the combo run was noise (17 ms here; base 28 ms
in this run). All four patches pass both gates: A, B, C, D kept.

## Final patches (named, not numbered)

| patch | was | fork `perf2-v1.4.0` |
|---|---|---|
| streaming-markdown | 001, 005, 006, 007, 008, 009 | e61293709 |
| highlight-cache | 002 + write renderer copy | b596cddb4, 05b47bf9d |
| streaming-tool-arguments | 003 | 4f0b1eccf |
| write-call-highlight | 004 + collapsed-path copy | a59d60900, 331f79e3a |

Not taken: 010 (live lines, +53 MB RSS), R1 (Pi-Bolt f2fae008c). The chain specs (`docs/pi-perf/0NN-*`, later
`v1.4.0-chain/`) were folded into the four specs and removed; the numbers above in this log refer to them.

### Can 005's string copies go? No (`drop-memory-parts-aot.txt`, 3 rounds + 1 warm-up)

Removing them from streaming-markdown (fork fb2339e35, reverted by a7e097bc1): md:60000 RSS 162.9 → 225.2 MB (+38%)
alone and 124.4 → 156.9 MB (+26%) with all patches; CPU unchanged. The ablation called 005 "no clear gain" from
md:20000 only, where the effect is small (+4.6%). Kept.

Order: all 64 ordered subsets apply and all 24 orders of the full set give the same tree (a5b9e5770…), checked with
`bench/check-patch-orders.ts`. highlight-cache's `write.ts` part had to be made with `-U1` for that.
