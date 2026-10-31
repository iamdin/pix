# Large images are resized with Bun.Image, not Photon

- Method: under Bun, image resizing uses Bun's native pipeline (`Bun.Image`) instead of Photon (WebAssembly).
- Verified on: Pi v1.4.0, `getpie-dev/pi` branch `pitch-v1.4.0` commit d95a12ee1
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
- `resizeImage()` in `image-resize.ts` uses it whenever `Bun.Image` exists, with no Photon fallback: the images
  that reach it are PNG, JPEG, GIF or WebP (`normalizeImage()` in `image-process.ts`), all of which Bun decodes. An
  image Bun cannot decode gives `null` ("could not be resized"), as `resizeImageInProcess()` does when Photon cannot.
  The Photon path stays for Node, where Pi's own tests run.
- `DEFAULT_OPTIONS` in `image-resize-core.ts` is exported for it.

Photon still converts images to PNG for Kitty terminals and for pasted non-PNG clipboard images; those are rare and
small, so they are left alone.

## Invariants

- Same result shape, dimensions, `wasResized` and format choice as Photon for the same input; the input is returned
  byte for byte when no resize is needed.
- Under Node (no `Bun.Image`) nothing changes.
- Undecodable input gives `null`, as with Photon.

## Tests

- Added `packages/coding-agent/test/image-resize-bun.bun.ts` (run with Bun: `bun test ./test/image-resize-bun.bun.ts`):
  unchanged small image equals Photon's result; 3000×1000 resizes to Photon's size and format; EXIF orientation 6
  gives Photon's dimensions; a tiny `maxBytes` is met; undecodable input gives `null` from `resizeImage()`, as from Photon.
- Must pass: `npx vitest run test/image-processing.test.ts test/image-resize-callers.test.ts`, root `npx tsc --noEmit`.
- End to end: `bun bench/e2e/run.ts <pitch build>/pi` ("print mode" reads a 3000 px image).

## Performance gate

`bench/e2e/run.ts` "print mode" on an AOT build: 25 s without, 0.33 s with (stock Pi 1.4 s). No other path changes.

## Skip when

Pi resizes images natively itself, or `Bun.Image` changes its API (`grep -n "Bun.Image" image-resize-bun.ts`), or
Bun AOT gets a WebAssembly JIT (then compare against Photon again).
