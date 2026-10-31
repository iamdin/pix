# Large images are resized with Bun.Image, not Photon

- Method: when the runtime has `Bun.Image`, image resizing uses Bun's native pipeline instead of Photon (WebAssembly).
- Verified on: Pi v1.4.0 (fork branch `native-image-resize`, commit 84002da18), diff `v1.4.0.diff`
- Independent: applies alone on the tag and with the other patches in any order (it touches only the image resize files).
- Reference: none; pitch's own.

## Problem

Bun's `--aot` executables run with every JavaScriptCore JIT off (`BUN_JSC_dumpOptions=2`: `useJIT=false`,
`useRegExpJIT=false`, and so on), so WebAssembly is interpreted. Pi resizes images over 2000 px (read tool, `@image`
attachments) with Photon, which is WebAssembly. Measured: one 2100×2100 PNG takes 20.5 s in an AOT binary and 0.6 s
under JIT; `bench/e2e/run.ts` "print mode" (one 3000 px image read) took 25 s on a pitch build, 1.4 s on stock Pi.

## Change

- New `packages/coding-agent/src/utils/image-resize-bun.ts`: `getBunImage()` finds `Bun.Image`, and
  `resizeImageWithBun()` follows `resizeImageInProcess()` step by step: return the input unchanged when it is within
  the limits; otherwise the same target size, then PNG and the same JPEG quality steps in the same order (taking the
  first under `maxBytes`), then shrink by 0.75. Width and height come from `metadata()`, which reports them after
  the EXIF orientation, as Photon does after `applyExifOrientation()`; `Bun.Image` applies the orientation itself.
- `resizeImage()` in `image-resize.ts` tries it first; when Bun cannot decode the image it rejects, and the old
  Photon worker/in-process path runs.
- `DEFAULT_OPTIONS` in `image-resize-core.ts` is exported for it.

Photon still converts images to PNG for Kitty terminals and for pasted non-PNG clipboard images; those are rare and
small, so they are left alone.

## Invariants

- Same result shape, dimensions, `wasResized` and format choice as Photon for the same input; the input is returned
  byte for byte when no resize is needed.
- Under Node (no `Bun.Image`) nothing changes.
- Undecodable input falls back to Photon.

## Tests

- Added `packages/coding-agent/test/image-resize-bun.bun.ts` (run with Bun: `bun test ./test/image-resize-bun.bun.ts`):
  unchanged small image equals Photon's result; 3000×1000 resizes to Photon's size and format; EXIF orientation 6
  gives Photon's dimensions; a tiny `maxBytes` is met; undecodable input rejects.
- Must pass: `npx vitest run test/image-processing.test.ts test/image-resize-callers.test.ts`, root `npx tsc --noEmit`.
- End to end: `bun bench/e2e/run.ts <pitch build>/pi` ("print mode" reads a 3000 px image).

## Performance gate

`bench/e2e/run.ts` "print mode" on an AOT build: 25 s without, 0.33 s with (stock Pi 1.4 s). No other path changes.

## Skip when

Pi resizes images natively itself, or `Bun.Image` changes its API (`grep -n "Bun.Image" image-resize-bun.ts`), or
Bun AOT gets a WebAssembly JIT (then compare against Photon again).
