# <one-line summary>

- Method: <the one optimization method, in one sentence>
- Verified on: Pi <tag> (fork commit <sha>), `getpie-dev/pi` branch `pix-<tag>` commit <sha>
- Independent: applies alone on the tag and with the other patches in any order

## Problem

What costs too much, in which workload, measured how (bench command and numbers), and the root cause.

## Change

The approach in words: which responsibility changes, and why it removes the cost. No line numbers: name the
functions, classes and data that are involved and how to find them (e.g. "the method that renders a Markdown
component", `grep -n "class Markdown" packages/tui/src/components/markdown.ts`).

## Invariants

What must stay exactly as before: output, screens, events, errors, memory bounds. Each one a test or a check below.

## Tests

- Added: <test file and what it proves; it must fail without the change where that is possible>
- Must pass: <commands>

## Performance gate

On AOT builds (`bench/build-variants.ts`), both must hold:

- Alone on the tag: no scenario worse than 5% (CPU, wall, RSS), and the expected gain.
- All patches vs all but this one: the expected gain (else it is not worth keeping).

    bun bench/run.ts --build base=<tag> --build this=<this alone> --build all=<all> --build no-this=<all but this> \
      --scenarios <...> --rounds 3 --expect "<scenario.metric><=<percent>" --max-regress 5

Plus what the numbers were when verified.

## Skip when

When upstream made the change unnecessary, or the code it changes is gone, and how to tell.
