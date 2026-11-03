# v1.5.0 log

Machine: Apple M4, 24 GiB, not pinned to cores. Bun 1.4.4-canary.1+271b65ec6 (pix's pin). All numbers are AOT builds
(`bench/build-variants.ts`, outputs in `pix/.work/pi-v1.5.0/`), medians; `pix140` is the v1.4.0 release set (5 patches,
`getpie/pix-v1.4.0` f9731d41a) rebuilt with the same Bun.

## What v1.5.0 changed upstream

- `f715a15c1` parses streamed tool-call arguments incrementally in every provider (`StreamingJsonParser`), and tool
  rows render streamed arguments once per frame.
- `0f21a3319` reuses highlighting (`CodeHighlighter` for write and codemode rows), keeps a Markdown component's
  last-rendered top-level blocks (keyed by their source) and its tokens, and `AssistantMessageComponent` keeps its
  Markdown components while a message streams.
- Images: PNG or JPEG, whichever is smaller (`e7a0df27c`); resizing is still Photon (WebAssembly).
- Still per chunk: a full marked lex of the message in `Markdown.render()`, and, with the default Mermaid mode
  `streaming`, a second full lex in the Mermaid transformer.

## Stock v1.5.0 vs pix v1.4.0 (`stock-vs-pix140.txt`, 3 rounds)

| scenario | pix140 | stock v1.5.0 |
|---|---|---|
| write:200 CPU | 0.110 s | 0.099 s |
| tuiwrite:200 CPU | 0.19 s | 0.11 s |
| md:20000 CPU / RSS | 1.33 s / 100 MB | 3.37 s / 149 MB |
| md:60000 CPU / RSS | 3.28 s / 130 MB | 18.85 s / 180 MB |

## Per patch

| patch | v1.5.0 | why |
|---|---|---|
| streaming-markdown | kept, 3b1e07642 | Upstream still lexes the whole text per change. Rebased onto upstream's Markdown: its source-keyed block map is replaced by this patch's token-keyed blocks; its highlight reuse across resizes stays as a per-component, weakly held memo; `Markdown.setComplete()` because components are now kept while streaming (spec steps 4 and 6). |
| highlight-cache | dropped (`retired/`) | Upstream reuses highlighting per component/row (skip condition). Step 3 (one thinking style) is moot: the thinking Markdown is created once per message now. Step 2 (Mermaid) is still needed: kept as the new patch mermaid-lex. |
| streaming-tool-arguments | dropped (`retired/`) | Skip condition: upstream parses streamed arguments incrementally. write:200 stock 0.099 s vs pix140 0.110 s. |
| write-call-highlight | dropped (`retired/`) | Stock tuiwrite:200 (0.11 s CPU) is already below pix140 (0.19 s); not ported and measured alone. Its skip condition does not strictly hold (a streamed write still highlights every line), so port and gate it if tuiwrite matters again. |
| native-image-resize | kept, a7682b874 | Upstream still resizes with Photon. Ported upstream's new candidate order (smaller of PNG/JPEG first) and its regression case to the Bun test. |
| mermaid-lex | new, b4d502d80 | The default Mermaid mode lexes the whole message per render while streaming. |

First try without mermaid-lex (`combos-aot.txt`): stock + streaming-markdown md:60000 14.6 s vs pix140 4.5 s.
`mermaid-aot.txt` found the Mermaid lex: stock 19.0 s, +streaming-markdown 9.4 s, +mermaid-lex 12.6 s, both 4.0 s.

## Every subset (`combos3-aot.txt`, 3 rounds; `small-aot.txt`, 15 rounds for the short scenarios)

md runs were noisy (stock md:20000 2.4–5.3 s within one run); the short scenarios were rerun with 15 rounds because
3 rounds gave a bimodal startup (0.023 / 0.044 s) and a 10 ms CPU tick on tuiwrite.

| | md:20000 CPU | md:60000 CPU | vs base |
|---|---|---|---|
| base (stock) | 4.93 s | 19.99 s | |
| streaming-markdown alone | 3.25 s | 13.61 s | −34% / −32% |
| mermaid-lex alone | 4.01 s | 15.12 s | −19% / −24% |
| all | 1.43 s | 4.47 s | −71% / −78% |
| all but streaming-markdown | 3.15 s | 16.30 s | all is −55% / −73% |
| all but mermaid-lex | 3.35 s | 12.64 s | all is −57% / −65% |

Short scenarios, 15 rounds: no subset worse than the tag beyond noise (startup, write:200 within ±3%; tuiwrite:200 CPU
0.13–0.14 s for every build, wall equal), all three below pix140 (write:200 −12%, tuiwrite:200 wall −20%).
native-image-resize does not touch these paths; its gate is the image read below.

## Against v1.4.0 (`vs-prev-aot.txt`, 5 rounds)

| scenario | pix140 | all v1.5.0 |
|---|---|---|
| md:20000 CPU / RSS | 0.88 s / 85.0 MB | 0.87 s / 85.8 MB |
| md:60000 CPU / RSS | 2.86 s / 113 MB | 2.99 s / 111 MB (+4.5% CPU, within the 5% gate; all-but-image 3.10 s in the same run) |

## End to end

`bench/e2e/run.ts` passes on stock v1.5.0 AOT and on the final build (`e2e-base.txt`, `e2e-final.txt`). Print mode
(one 3000 px image read): 26.1 s stock, 0.67 s with the patches.

`pix self prepare` of b4d502d80 ran every patch's tests (`piTests`); `pix compile --pi` passed its checks.

## Tests

On the branch: `packages/tui` `npm test` passes; coding-agent `streaming-markdown-render`, `assistant-message`,
`mermaid`, `image-processing`, `image-resize-callers` pass; `bun test ./test/image-resize-bun.bun.ts` passes (the
new JPEG case fails without the port). Root `tsc --noEmit` reports only errors in `packages/ai/test` (model ID
unions), which v1.5.0 has too.
