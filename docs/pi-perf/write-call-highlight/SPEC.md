# A collapsed write call highlights the lines it shows, not the whole file

- Verified on: Pi v1.4.0, `getpie-dev/pi` branch `pitch-v1.4.0` commit b577999cb
- Method: a collapsed write call highlights only the lines it can show.
- Independent: applies alone on the tag and with the other patches in any order. Its gain shows with
  streaming-tool-arguments; without it the argument parsing dominates.
- Reference: Pi-Bolt 24d079015 (MIT, © OpenSec). Supersedes pitch's old builtin P4 (completed-write-cache) for the
  collapsed case.

## Problem

The `write` tool's call renderer (`packages/coding-agent/src/core/tools/renderers/write.ts`, `writeRenderers.renderCall`)
keeps a highlight cache (`WriteHighlightCache`) that highlights every line of the file as it streams in, and highlights the
whole file again when the arguments are complete — even though a collapsed call shows only 10 lines.

Measured with streaming-tool-arguments (JIT): `tuiwrite:400` (TUI, 400 KiB file) 1.05 s CPU, 0.66 s wall.

## Change

In `write.ts`:

1. The cache records whether all lines are highlighted (`all`), whether it was built from the complete file (`complete`),
   and, once the call has its result and is collapsed, the total line count (`lineCount`).
2. Collapsed (`!context.expanded`): build/extend the cache keeping all normalized lines but highlighting only the first
   `WRITE_PARTIAL_FULL_HIGHLIGHT_LINES` (50); `all` is true only when the file has no more lines than that. Display the
   first `WRITE_COLLAPSED_LINES` (10) highlighted lines and count the total lines as the full path does (trailing empty
   lines dropped, unless an empty line highlights to something non-empty).
3. Expanded: highlight the remaining lines one by one (`highlightRemainingLines`) or rebuild in full, as before.
4. Arguments complete and collapsed: reuse the cache when it was built from this same complete content; otherwise rebuild
   collapsed and mark it complete. Never highlight the whole file for a collapsed call.
5. When the call is finished (`!context.isPartial`) and collapsed, store `lineCount` and cut `normalizedLines` to the first
   50, so finished writes in a long session do not keep every file split into lines.
6. `status-indicator.ts`, `StatusIndicator.renderInBorder()`: keep the last `{line, width, text}` and return it when the
   spinner line and width are unchanged (the editor border is drawn every frame).
7. Collapsed and streaming: copy the text added since the last render (`JSON.parse(JSON.stringify(...))`) before
   splitting it into lines. In JSC a slice shares its whole source string, and without streaming-tool-arguments `fileContent` is a new copy of
   the file on every chunk. Upstream, highlighting every line flattened those strings; lines past the first 50 are no
   longer highlighted, so without the copy each kept an earlier copy of the file alive: tuiwrite:200 without streaming-tool-arguments went from
   319 MB to 2.8 GB RSS (AOT, `results/v1.4.0/log.md`, "Every subset on AOT").

## Invariants

- The collapsed call shows exactly the lines and "… (N more lines, M total" text the expanded/full path would show, at
  every streaming step, for highlighted and plain languages.
- Expanding at any point shows the fully highlighted file; collapsing again works.
- This patch alone uses no more memory than the tag in any scenario (tuiwrite:200 included: step 7).

## Tests

- Added `packages/coding-agent/test/write-call-collapsed-highlight.test.ts`: streamed in steps of several sizes, collapsed
  output equals what the expanded call derives; expand/collapse while streaming; a large complete file is not highlighted.
- Must pass: `cd packages/coding-agent && npx vitest run test/write-call-collapsed-highlight.test.ts` plus the existing
  `test/write*` / `test/tool-execution*` tests; `npx tsc --noEmit`.

## Performance gate

    bun bench/run.ts --build base=<streaming-tool-arguments> --build new=<streaming-tool-arguments + this> --scenarios startup,tuiwrite:400,md:20000 \
      --rounds 5 --expect "tuiwrite:400.cpu_s<=-10" --max-regress 5

And this alone vs tag, all vs all but this (no scenario worse than 5%; AOT, `bench/build-variants.ts`).

v1.4.0, JIT (`results/v1.4.0/004.txt`): tuiwrite:400 CPU 1.05 → 0.82 s (−21.9%), wall 0.66 → 0.48 s, RSS 299 → 251 MB.
Gate passed.

## Skip when

Upstream's write renderer no longer highlights lines a collapsed call does not show.
