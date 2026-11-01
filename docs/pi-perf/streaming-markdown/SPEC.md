# Streaming Markdown renders only what changed

- Method: a streaming Markdown reply is lexed, rendered, wrapped and joined incrementally, so the cost of a chunk does not
  grow with the length of the reply.
- Verified on: Pi v1.4.0, `getpie-dev/pi` branch `pix-v1.4.0` commit 40b48953a
- Independent: applies alone on the tag and with the other patches in any order.
- Implement the parts below in order: each builds on the one before.

## Problem

While an assistant message streams, each chunk lexes the whole text again, renders every block again, and copies every
line through every container. The cost of a chunk grows with the length of the reply, so a long reply costs O(n²).
v1.4.0 AOT: md:20000 6.4–6.6 s CPU, md:60000 24.2 s.

## Parts

### Lex and render a streaming Markdown message from its last blocks on

#### Problem

While an assistant message streams, the interactive mode builds a new `Markdown` component for each chunk
(`AssistantMessageComponent.updateContent()` → `new Markdown(...)`), so the component's own caches never hit: every chunk
lexes the whole text with marked and renders, wraps and pads every block again. The cost of a chunk grows with the length
of the message, so a long answer costs O(n²).

Measured on v1.4.0, `bench/run.ts --scenarios md:20000` (160×48 TUI, 24-character chunks every 20 ms), 5 rounds:
8.32 s CPU [8.09–8.43] for the answer.

#### Change

All in `packages/tui/src/components/markdown.ts` unless named.

1. **Lex from the previous source** (module-level, not per component, because components are recreated):
   - Keep the last few sources lexed (`lexedMarkdown`, 8 entries) with their tokens and whether lexing can resume from them.
   - When a new source is a kept source with text appended, keep that source's top-level tokens except the last two
     non-`space` blocks, and lex only the text from there (`lexAddedMarkdown()`). The last block may still be on its first
     line, and where the block before it ends depends on the line after it.
   - Fall back to a full lex when: the earlier tokens contain link reference definitions (`tokens.links` non-empty; they
     change how earlier links read), the token `raw` lengths do not add up to the source (the lexer rewrote line endings), or
     there are fewer than three blocks.
   - The same source lexed again returns the same token array (identity matters for step 2).
   - `trimPartialClosingFences()` still runs on the result.
2. **Render per block, keyed by the token object**: `renderBlock(token, nextTokenType, width, contentWidth)` renders,
   wraps (`wrapTextWithAnsi`), adds margins and background or padding for one top-level token, and stores the lines in a
   `WeakMap<Token, …>` together with every input besides the token: width, paddingX, next token type, theme object,
   default text style fields, `preserveOrderedListMarkers`, `preserveBackslashEscapes`, `renderLatex`, terminal capabilities
   object, and a generation number. `render()` concatenates the blocks' lines; image lines are kept as they are.
3. **Generation** (`packages/tui/src/rendered-markdown.ts`, exported from the package index as
   `invalidateRenderedMarkdown`): a counter. `Markdown.invalidate()` and `TuiBase.invalidate()` (in `tui.ts`) bump it,
   because theme functions may return something else after a theme change. `Markdown.setText()` no longer calls
   `invalidate()`; it only clears the component's own cached text/width/lines.
4. **A finished message is lexed in full**: `MarkdownOptions.complete`; when set, `lexMarkdown(source, true)` never pieces
   tokens together from a shorter source, and does not return an entry that was pieced. `AssistantMessageComponent` passes
   `complete: !this.isStreaming` for both text and thinking Markdown.
5. Test hooks: `setMarkdownCachingForTest(enabled)` (off = lex everything, keep nothing) and `lexMarkdownForTest(source,
   complete)`.

#### Invariants

- For every prefix of a document, the lines rendered with caching equal the lines rendered with caching off, except where
  piecewise lexing splits a block that a full lex keeps whole (a `2.` line after a paragraph line, `$$` math across blank
  lines). This may show for a moment while streaming; the finished message (`complete`) is always the full lex.
- Theme change, `/reload`, resize: output equals a fresh render (generation, width and theme are part of the key).
- Link reference definitions anywhere: output equals a full lex.
- CRLF / lone CR text: output equals a full lex.

#### Tests

- Added `packages/tui/test/markdown-incremental-lex.test.ts`: streams documents of every block kind one character at a time
  and in random chunks, comparing tokens and rendered lines with caching off; plus the `complete` cases.
- Must pass: `cd packages/tui && npm test`; `cd packages/coding-agent && npx vitest run test/assistant-message.test.ts
  test/mermaid.test.ts`; `npx tsc --noEmit` at the repository root.

### A streamed Markdown block no longer keeps the whole answer alive

#### Problem

With incremental lexing, the tail of a growing message is lexed as `source.slice(offset)`. JavaScriptCore (and V8) make a slice share the
parent string's memory, and marked's tokens keep slices of what they lexed, so every block lexed during streaming keeps
the whole message, as it was at that moment, alive: megabytes per long answer, for each of the kept answers. Also the
whole text's tabs are replaced again on every render.

Measured (JIT, before this part): md:60000 RSS after the answer 245.6 MB [244.4–248.6].

#### Change

In `packages/tui/src/components/markdown.ts`:

1. In `lexAddedMarkdown()`, lex `ownString(source.slice(offset))`, where `ownString(text)` returns
   `JSON.parse(JSON.stringify(text))`: a fresh string that shares no memory with the source.
2. `replaceTabs(text)`: keep the last 4 `{text, replaced}` pairs; when a new text extends a kept one, reuse its replaced
   prefix and replace tabs only in the added part. Return the text itself when it has no tab. With caching off (test hook),
   replace in full. `render()` uses it instead of `text.replace(/\t/g, "   ")`.
3. The empty-text check uses `!/\S/.test(text)` instead of `text.trim() === ""` (no copy of the text).

#### Invariants

- Rendered lines are unchanged (the incremental-lex test still passes, including tab documents).
- Memory per finished answer no longer includes copies of the whole answer per block.

#### Tests

- `packages/tui/test/markdown-incremental-lex.test.ts` gains a tab case. Must pass: `cd packages/tui && npm test`;
  `npx tsc --noEmit`.

### Containers keep their lines between frames

#### Problem

`Container.render()` (`packages/tui/src/tui.ts`) builds a new array from every child's lines on every frame, and `Box`
re-pads and compares every line, so in fullscreen mode a frame costs time in proportion to the whole transcript even when
only the last message changed.

#### Change

1. `tui.ts`:
   - `markStableRender(componentClass)`: a class declares that its `render()` returns a new array whenever its lines
     change and never modifies an array it returned. `isStableRender(component)`: declared, or a `Container` that does
     not override `render()`.
   - `Container.renderShared(width)`: keeps the joined lines of its children and returns the same array while no child's
     lines changed; when one changed, keeps the unchanged leading children's lines and joins from there. A stable child is
     unchanged when it returned the same array; any other child is compared line by line with what was joined the frame
     before (so a component that mutates its array in place, e.g. an extension's, renders as before).
     `Container.render()` still returns a copy. `renderSharedLines(component, width)` reads shared lines without the copy.
2. Stable classes: `Text`, `Markdown`, `Image`, `Spacer` (keeps its lines), `Box` (reuses padded lines of unchanged child
   lines), `MouseRegion`, plain `Container`.
3. The fullscreen layout (`layout.ts`) and `tui-alt-screen.ts` read children through `renderSharedLines`.
4. Export `isStableRender`, `markStableRender`, `renderSharedLines` from the package index.

#### Invariants

- Every frame renders exactly the lines it rendered before, for stable and non-stable children, including children that
  mutate arrays in place and children added/removed/replaced.
- Arrays handed to callers of `render()` are never modified later.

#### Tests

- Added `packages/tui/test/render-memo.test.ts`. Must pass: `cd packages/tui && npm test`; `npx tsc --noEmit`.

### A streaming message's frame no longer costs time in proportion to its length

#### Problem

With incremental lexing and container line reuse, each chunk of a long answer still does work over the whole message: the container compares a stable
child that returned a new array line by line with the previous frame (at every container level) and joins lines one
`push()` at a time; `Markdown.render()` visits every top-level block's cache and pushes every line again; and
`lexAddedMarkdown()` sums the lengths of all kept tokens.

#### Change

1. `tui.ts` `renderChildren()` / `Container.renderShared()`: a stable child (container line reuse) that returned a new array is taken as
   changed without comparing its lines; lines are joined with `concat()`.
2. `components/markdown.ts`:
   - `lexedFrom: WeakMap<Token[], {tokens: WeakRef<Token[]>, shared}>`: token lists pieced from a shorter source's tokens
     record which list they came from and how many leading tokens they share.
   - `renderedTokens: WeakMap<Token[], …>`: the lines of all blocks of a token list (with every render input, as for one block).
     A new list takes the shared blocks' lines from its source list in one `slice()` and renders only the blocks lexed
     again.
   - The offset of the kept tokens is counted from the end of the source instead of summing all raw lengths.
3. Test `packages/coding-agent/test/streaming-markdown-render.test.ts` streams replies through the real assistant message
   component and compares every frame with a render from nothing.

#### Invariants

As for incremental lexing: every frame equals a render without caches (except its documented transient split), at all widths and themes.

#### Tests

- Added `packages/coding-agent/test/streaming-markdown-render.test.ts`; `packages/tui` tests updated.
- Must pass: `cd packages/tui && npm test`; `cd packages/coding-agent && npx vitest run test/streaming-markdown-render.test.ts
  test/assistant-message.test.ts`; `npx tsc --noEmit`.

### A streaming reply's settled blocks are not lexed and rendered again for each chunk

#### Problem

Incremental lexing re-lexes the last two top-level blocks on every chunk and renders both to new lines (highlighting included), although
the second-to-last block has usually settled.

#### Change

In `packages/tui/src/components/markdown.ts`, `lexAddedMarkdown()`:

1. After re-lexing, a token that is equal field by field to the token at the same position before is replaced by the
   previous token object, so its rendered lines (keyed by token identity) are kept.
2. When the last block is past its first non-blank line and the block before it is a kind marked tries before a paragraph
   (code, heading, hr, list, blockquote, table, html), only the last block is lexed again.

#### Invariants

Every rendered frame equals the previous algorithm's (fuzz-tested over random documents, chunkings and widths).

#### Tests

- `packages/tui/test/markdown-incremental-lex.test.ts` and `packages/coding-agent/test/streaming-markdown-render.test.ts`
  extended (400 random documents per run). Must pass: as for the frame part above.

### Wrap and lex only what changed in a streaming Markdown block

#### Problem

The block a chunk ends in (often a long list or code block) is rendered again in full on every chunk: every line wrapped
again, every inline text lexed again, though all but the last few lines are unchanged.

#### Change

1. `components/markdown.ts`:
   - Per component, keep the lines wrapped in the last render that rendered a block, by width; wrap only lines not wrapped
     then (`wrapTextWithAnsi()` is a pure function of line and width).
   - Keep the inline tokens of the texts lexed by the last lex, keyed by the text and the lexer state before it (`inLink`,
     `inRawBlock`, `linkEmitted`), with the state after it. Not kept/used: a text containing `[` in a document with link
     definitions, texts lexed inside other texts, texts given tokens to add to (a task checkbox). Nothing is kept after a
     complete text is lexed.
   - `trimPartialClosingFences()` takes a code block's last line without splitting the block into lines.
2. `utils.ts` `wrapTextWithAnsi()`: a text without line breaks is not split, and the styles of its last line are not
   tracked.

#### Invariants

Output and token lists equal those with the memos off, for random documents with links, link definitions, raw `<a>` and
`<pre>`, tasks, tables, highlighted code and changing widths.

#### Tests

- Added `packages/tui/test/markdown-render-memo.test.ts` (fails when the lexer state, the link check, the width or the
  prefilled-tokens check is removed). Must pass: as for the frame part above.

## Performance gate

On AOT builds (`bench/build-variants.ts`):

    bun bench/run.ts --build base=<tag> --build md=<this alone> --build all=<all> --build no-md=<all but this> \
      --scenarios startup,write:200,tuiwrite:200,md:20000,md:60000 --rounds 3

Alone vs tag: md:20000 CPU at least −30%, nothing worse than 5%. All vs all but this: md:20000 CPU at least −50%.

v1.4.0 (`results/v1.4.0/log.md`, "Every subset on AOT"): alone md:20000 −45%, md:60000 −33%; all vs all but this
md:20000 −72%, md:60000 −77%. The container line reuse part has no gain of its own; the frame part is built on it. Without the part that keeps
a block from holding the whole answer, md:60000 RSS goes up 38% alone and 26% with all patches
(`results/v1.4.0/drop-memory-parts-aot.txt`).

## Not taken

- Live lines: −7% CPU at md:60000 but +53 MB RSS (+37%).

## Skip when

Upstream renders streaming Markdown incrementally itself: `AssistantMessageComponent` no longer creates a new `Markdown` per
update and `render()` no longer lexes the full text, or marked gains incremental lexing that Pi uses. Per part: skip the
container part when containers already reuse their joined lines between frames; skip the wrap/inline-lex part when
`wrapTextWithAnsi` and marked inline lexing are no longer per-render costs.
