# Parse streaming tool-call arguments geometrically, not on every delta

- Verified on: Pi v1.4.0, `getpie-dev/pi` branch `pix-v1.4.0` commit 57f01a394
- Method: a growing tool-call argument is parsed at geometrically spaced sizes, not on every delta.
- Independent: applies alone on the tag and with the other patches in any order.
- Reference: pix's earlier P3 (geometric tool arguments).

## Problem

Every provider's stream handler (`packages/ai/src/api/*.ts`) appends each tool-call delta to `block.partialJson` and then
calls `parseStreamingJson(block.partialJson)` on the whole text. A large argument (a file passed to `write`) arrives in
thousands of small deltas, so parsing is quadratic in its size.

Measured (JIT, with streaming-markdown and highlight-cache): `write:200` (`pi -p`, 200 KiB file, 16-character deltas) 27.7 s CPU, 13.3 s wall, 426 MB peak.

## Change

1. `packages/ai/src/utils/json-parse.ts`: `parseStreamingJsonWhileStreaming(block, partialJson, current)`. A `WeakMap`
   from the block object to the length last parsed. Below 1024 characters, parse on every delta as before. From then on,
   return `current` unchanged until the text has grown by at least an eighth of the length last parsed (or shrank), then
   parse in full with `parseStreamingJson` and record the length.
2. Every provider that parses streaming tool-call deltas uses it instead of `parseStreamingJson` (grep
   `parseStreamingJson(` under `packages/ai/src/api/`; on v1.4.0: anthropic-messages, bedrock-converse-stream,
   mistral-conversations, openai-completions, openai-responses-shared, pi-messages). Final arguments are still parsed in
   full when the tool call ends (`parseToolCallArguments`), and where a stream can end without the block's stop event
   (Bedrock: the end-of-stream loop over `blocks`), the final parse is added there.
3. Only append-only deltas use it; a replacement of the whole argument text keeps calling `parseStreamingJson`.

## Invariants

- The final arguments of every tool call are exactly what a full parse of the complete text gives, on every provider,
  including streams that end without a block stop.
- Small arguments (< 1 KB) update on every delta as before. Large ones still update while streaming (every ≥12.5% growth).

## Tests

- Added `packages/ai/test/streaming-json-budget.test.ts`: small prefixes parse every time; total re-parsed input is
  bounded for tiny deltas and the final arguments are unchanged; interleaved calls with equal ids stay apart; first parse,
  absent value and shorter restart are never skipped; final validation is separate from the cached prefix.
- Added `packages/ai/test/streaming-json-providers.test.ts`: per provider, every raw delta is kept and the terminal call is
  fully parsed; malformed final JSON is rejected despite a usable cached prefix; same-length final replacement is
  accepted; Bedrock finalizes without `contentBlockStop`.
- Must pass: `cd packages/ai && npx vitest run test/streaming-json-budget.test.ts test/streaming-json-providers.test.ts
  test/json-parse.test.ts test/openai-responses-partial-json-cleanup.test.ts`; `npx tsc --noEmit`.

## Performance gate

    bun bench/run.ts --build base=<tag> --build new=<this alone> --scenarios startup,write:200,md:20000 \
      --rounds 5 --expect "write:200.cpu_s<=-50" --max-regress 5

And all vs all but this, same scenarios plus tuiwrite:200 (AOT, `bench/build-variants.ts`).

v1.4.0, JIT (`results/v1.4.0/003.txt`): write:200 CPU 27.66 → 0.62 s (−97.8%), wall 13.3 → 0.26 s, peak RSS 426 → 168 MB.
Gate passed.

## Skip when

Upstream parses streaming arguments incrementally itself, or the providers no longer re-parse `partialJson` per delta.
