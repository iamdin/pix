# No Mermaid lex of a message without the word mermaid

- Method: the Mermaid Markdown transformer does not lex a message to look for Mermaid blocks when the text cannot have one.
- Verified on: Pi v1.5.0, `getpie-dev/pi` branch `pix-v1.5.0` commit b4d502d80
- Independent: applies alone on the tag and with the other patches in any order. Its gain is largest with
  streaming-markdown, which makes the rest of a chunk's work cheap.
- History: step 2 of pix's highlight-cache on v1.4.0 (`docs/pi-perf/retired/highlight-cache/`); its other steps were
  dropped on v1.5.0, which reuses highlighting itself.

## Problem

Pi's default Mermaid mode is `streaming` (`settings-defaults.ts`, `markdown.mermaid`). Then the transformer
(`createMermaidMarkdownTransformer`, `packages/coding-agent/src/modes/interactive/components/mermaid.ts`) lexes the
whole assistant message with marked on every render, also while it streams, to find `mermaid` code blocks and join
the tokens' `raw` again. A long answer pays a full lex per chunk: O(n²).

Measured on v1.5.0 AOT (`results/v1.5.0/mermaid-aot.txt`): md:60000 CPU 19.0 s on the tag, 9.4 s with
streaming-markdown, 4.0 s with streaming-markdown and this.

## Change

In the transformer, after the existing mode checks: return the input unchanged when `/mermaid/i` does not match it (a
Mermaid block names its language in its fence).

## Invariants

- Markdown without the word "mermaid" renders identically: the transformer used to return the tokens' `raw` joined,
  which is the input (the Markdown component normalizes line endings itself).
- Text with the word renders as before (the lex runs).

## Tests

- Added to `packages/coding-agent/test/mermaid.test.ts`: text without the word comes back as the same string, streaming
  or not.
- Must pass: `cd packages/coding-agent && npx vitest run test/mermaid.test.ts test/assistant-message.test.ts`.

## Performance gate

On AOT builds: this alone vs tag, and all vs all but this, scenarios `startup,write:200,tuiwrite:200,md:20000,md:60000`,
no scenario more than 5% worse; expect md:60000 CPU at least −30% for all vs all but this.

v1.5.0: see `results/v1.5.0/log.md`.

## Skip when

The transformer no longer lexes the message per render (it checks for a Mermaid fence itself, or caches its result per
source), or the default mode is no longer `streaming` and lexing a finished message once is all it does.
