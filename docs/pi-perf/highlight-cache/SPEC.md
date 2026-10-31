# Keep highlighted code; no Mermaid lex without Mermaid; one thinking style

- Method: work whose input did not change (highlighting a code block, looking for Mermaid, the thinking style) is not
  done again on each render.
- Verified on: Pi v1.4.0, `getpie-dev/pi` branch `pitch-v1.4.0` commit 31dc37c9a
- Independent: applies alone on the tag and with the other patches in any order. Alone it saves the
  re-highlighting; its biggest gain is with streaming-markdown, whose per-block cache needs stable styles and cheap code
  blocks.
- Supersedes pitch's old builtin P1 (bounded-highlight-cache).

## Problem

A streaming answer still pays per chunk for:

1. Every code block of the message being highlighted again (highlight.js plus styling) — the block being written changes,
   the others do not.
2. The Mermaid Markdown transformer (`createMermaidMarkdownTransformer`, `components/mermaid.ts`) lexing the whole message
   with marked on every render to look for `mermaid` code blocks.
3. The thinking block's default text style being a new closure on every render (`color: (text) => theme.fg(...)` in
   `AssistantMessageComponent`), so streaming-markdown's per-block cache, keyed by the style's `color` function, never hits for thinking.

Measured with streaming-markdown (JIT): md:20000 5.45 s CPU [5.39–5.59].

## Change

1. In `packages/coding-agent/src/modes/interactive/theme/theme.ts`: a module-level LRU of highlighted lines keyed by
   `lang + "\n" + code`, for the theme in use (read through the global `THEME_KEY`, since `theme` is a proxy); cleared when
   the theme in use changes. Bounds: 64 entries and 512 K characters of keys; entries over a quarter of the bound are not
   kept. When a new key extends the previous key (the block being written), the shorter one is dropped. Hits return a copy
   of the array. Used by both `highlightCode()` and the Markdown theme's `highlightCode` from `getMarkdownTheme()`; errors
   from highlight still fall back to plain lines as before.
2. In the Mermaid transformer: after the existing mode checks, return the input unchanged when `/mermaid/i` does not match
   it (a Mermaid block names its language).
3. In `assistant-message.ts`: the thinking style's `color` is one module-level function.

4. In the write call renderer (`packages/coding-agent/src/core/tools/renderers/write.ts`, the function that extends the
   highlight cache with the text added since the last render): copy the added text (`JSON.parse(JSON.stringify(...))`)
   instead of keeping `fileContent.slice(...)`. In JSC a slice shares its whole source string. Without streaming-tool-arguments the arguments
   are parsed in full on each chunk, so `fileContent` is a new copy of the file each time, and every line built from a
   slice kept one alive. Upstream, highlight.js reading each line through a regex flattened those ropes as a side
   effect; step 1 skips that for cached lines. Measured without streaming-tool-arguments (tuiwrite:200, JIT): 3.1 GB RSS without this step,
   610 MB with it (`results/v1.4.0/log.md`, "Why 002 without 003 uses GBs" (002 = this patch, 003 = streaming-tool-arguments)).

## Invariants

- Highlighted output is identical to highlighting afresh, for every language, theme, and after a theme switch.
- Memory stays bounded (64 entries / 512 K characters), and this patch alone uses no more memory than the tag in any
  scenario (tuiwrite:200 included: step 4).
- Markdown without the word "mermaid" renders identically (the transformer used to return the tokens' `raw` joined, which
  equals the input except CRLF, which the Markdown component normalizes anyway).

## Tests

- Added `packages/coding-agent/test/highlight-code-cache.test.ts`: same output as uncached, theme switch, bounds.
- Must pass: `cd packages/coding-agent && npx vitest run test/highlight-code-cache.test.ts test/assistant-message.test.ts
  test/mermaid.test.ts test/syntax-highlight.test.ts`; `npx tsc --noEmit`.

## Performance gate

On AOT builds: this alone vs tag, and all vs all but this, scenarios `startup,write:200,tuiwrite:200,md:20000`, `--max-regress 5`
(RSS included). Expect md:20000 CPU at least −30% for all vs all but this.

v1.4.0 ("Every subset on AOT" in `results/v1.4.0/log.md`): alone md:20000 −15%, tuiwrite:200 CPU −36%, RSS +3%; all
vs all but this md:20000 −59%, md:60000 −71%. Without step 4 and without streaming-tool-arguments, tuiwrite:200 reached
3.7–4.4 GB RSS (AOT).

## Skip when

Upstream caches highlighted code itself, or the Markdown theme no longer highlights code blocks through
`highlightCode`/`getMarkdownTheme().highlightCode`. Skip step 2 if the Mermaid transformer no longer lexes the full text.
Step 4 stays as long as the write renderer builds lines from slices of the streamed content.
