# pitch

Pi, built ahead of time with official Bun AOT, with performance patches and your extensions compiled in.
**Currently alpha.10, for macOS ARM64, Linux x64 and Linux ARM64 (glibc). Still an experimental toolchain.**

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/iamdin/pitch/main/install.sh | sh
```

Installs to `~/.local/bin/pitch` (change it with `PITCH_INSTALL_DIR`) and checks the SHA-256. pitch carries Bun and a Pi
that is already patched, built and tested, so `pitch build` only compiles your extensions on your machine, in about a
minute, and needs no Node. Extensions from npm are downloaded with npm, from Git with git. Nothing global is changed.

From source: `go build -trimpath -o bin/pitch ./cmd/pitch` (Go 1.24+, and the Bun archive for your platform in
`internal/pitch/runtime-data/<goos>-<goarch>/bun.zip`, which is not in git). Such a development build has no Pi in it:
run `pitch prepare /abs/pi.tar.gz` first (needs Git, Node 24+ and npm 11+), then `pitch build --pi /abs/pi.tar.gz --out
DIR`. `bun scripts/release.ts` builds Pi into pitch.

## Use

```sh
pitch build              # build with the extensions in ~/.pi/agent/settings.json (again after they change)
pitch pi                 # run the build; everything after pi goes to Pi: pitch pi -c, pitch pi install npm:x
pitch status             # version, patches, compiled-in extensions, whether the settings changed
pitch off / pitch on     # run the original Pi instead / the pitch build again (once: PITCH_OFF=1 pitch pi)
pitch uninstall          # remove ~/.pitch, its builds and the PATH line it added
pitch feedback FILE      # send a failed build's feedback (it names FILE) as a GitHub issue, after you confirm

pitch build --name pi    # also install a `pi` command that takes the place of the original Pi; it is `pitch pi`
```

- The original Pi is not changed. `pitch pi install/remove/uninstall/update/config` run the original Pi, which then
  reminds you to run `pitch build`.
- When the original Pi is upgraded past the version pitch builds, `pitch pi` runs the original and says so in one line;
  run `pitch build` again once pitch has caught up.
- A new build is switched to only after it passes its checks. The previous build is kept for sessions still running it;
  older ones are deleted along with their build directories in the cache.
- To upgrade pitch, run the install command again.
- `build` shows how long each step took, says every minute that compiling is still running and stops it after 30
  minutes (`--timeout`, `0` for no limit). An extension with a very large function can compile for a long time; leave
  one out with `{"source": "...", "extensions": []}` in the settings to find it.
- A failed build writes feedback for a GitHub issue next to its log, with your home directory replaced by `~`, and sends
  nothing. `pitch feedback FILE` shows it and asks before sending it with the GitHub CLI when `gh` is logged in;
  otherwise it prints a prefilled link to submit in the browser. `-y` sends without asking, for an agent that has shown
  the feedback to you and has your agreement.

## Build

```sh
pitch build --out ./dist/my-pi
./dist/my-pi/pi

# a new settings file, in the format of the packages in Pi's settings
pitch build --settings ./pitch-settings.json --out ./dist/my-pi-ext
```

Example `pitch-settings.json`:

```json
{
  "packages": [
    "npm:@getpie/pi-loop@0.0.7",
    "git:github.com/example/tools@v1.0.0",
    "./local-tools",
    { "source": "npm:another-package", "extensions": [] }
  ]
}
```

The Git source and another-package are placeholders: replace or remove them. Only `packages` is read; models,
credentials and other settings are not copied. Relative local paths resolve from the directory of the settings file.
User settings are not read and installed extensions are not scanned unless you ask for it.

Sources can also be given directly:

```sh
./bin/pitch build --extension npm:@getpie/pi-loop --out ./dist/pi-loop
./dist/pi-loop/pi
```

`--extension` can be repeated; with `--settings` it appends to the declared list. Local sources on the command line
resolve from the working directory. The old `NAME=/entry.ts` is still accepted, but NAME no longer makes a run-time
alias. The output directory must not exist: failed or existing outputs are never overwritten.

## From Pi's declarations to AOT

1. Pi's own package manager, at the pinned version, handles npm, Git/URL and local directory or file sources with
   Pi's source syntax. npm versions and Git refs are kept.
2. Pi's `packages` schema, package de-duplication, manifest and directory discovery and resource filters are reused,
   including `extensions: []` on objects, globs, exclusions and `autoload`. No new package identity protocol.
3. Dependencies are installed in a temporary project in its own build directory, away from your installation and from
   Pi's old global npm fallback. Local packages do not get dependencies installed and are not changed.
4. Pi and the selected extension entries are compiled as several Bun entry points with
   `--compile --aot --compile-in-place`. No extension factory aggregation, no custom builtin names.
5. The generated entry passes the compiled paths to Pi's own `-e`. **Compiled-in extensions load automatically; there
   is nothing to give at run time.**

The Go CLI orchestrates; a small JS bridge calls this Pi's package manager instead of reimplementing its npm and Git
resolution. Local sources are made absolute against the settings directory with the pinned Pi's internal resolver, so
upgrading Pi must run the resolution regression tests.

## Loading and safety

- Compiled-in extensions run, top-level module code and factories included; the help and RPC smoke checks during the
  build may run them too. **Build trusted packages only. The isolated HOME and directories are not a sandbox.**
- Only the extensions selected at build time load automatically. The entry still passes `--no-extensions`, so other
  extensions in user, project or settings are not run; `-e` at run time can still load an extension from disk, but it
  is not AOT code.
- No `builtin:pitch-NAME`, no new extension URI or ID protocol. Extension tool and command names are not rewritten;
  conflicts are Pi's to handle.
- Skills, prompts, themes, models, credentials and sessions come from your normal configuration; skills, prompts and
  themes inside built packages are not distributed with the build.
- npm lifecycle scripts are disabled and user or global npm configuration is not read. Packages that need install
  scripts or private registry credentials may not build; compile-time code such as Bun macros may still run.
- Management commands (a first argument of install/remove/uninstall/update/list/config/auth/mcp) are refused by a build
  run directly. Do not let a self-update overwrite a custom build.

## Pinned toolchain and optimizations

| Component | Pinned |
|---|---|
| Pi | v1.4.0 with pitch's patches: [getpie-dev/pi](https://github.com/getpie-dev/pi) `pitch-v1.4.0`, f9731d41a29da59e2893622d7189ce85840cf137 |
| Bun | 1.4.4-canary.1, da867c60bf0bcbf9d6d36994f8ca0efa42075115 |
| WebKit/JSC | 8c6c963e3c49fe39942d9625d1fc924128fad26e |

There is no choice of Pi or Bun version. Pi follows upstream release tags only, and pitch's patches are commits on a
fork (see [PATCHES.md](PATCHES.md)). Bun AOT exists only in canary builds, so Bun is pinned to one official canary build
and moves when the maintainers upgrade it. Each platform's release embeds that platform's pinned Bun archive and the
prepared Pi (about 110 MiB) and checks the archive and binary hashes and the revision, and on macOS the code signature.
Nothing depends on upstream downloads that may be deleted, and nothing upgrades silently. Cold fetches of Git, npm and
the model catalog still need the network. The Bun is the official prebuilt one, not a custom-optimized Bun/JSC.

**Pi comes with 5 optimization patches that passed AOT performance gates.** Each is one optimization method and one
commit on the fork, and its tests run at every release. Method, invariants and measurements are in
`docs/pi-perf/<name>/SPEC.md`, results in `docs/pi-perf/results/v1.4.0/log.md`.

| Patch | Method |
|---|---|
| `streaming-markdown` | A streamed reply re-lexes and re-renders only the blocks that changed |
| `highlight-cache` | Highlighted code blocks are kept instead of highlighted again each frame |
| `streaming-tool-arguments` | Streamed tool arguments are re-parsed at geometrically spaced sizes |
| `write-call-highlight` | A collapsed write call highlights only the lines it shows |
| `native-image-resize` | Large images are resized natively with Bun.Image, not Photon (without a JIT, its WebAssembly is tens of times slower) |

Pi v1.4.0 with official AOT, stock → pitch (Apple M4, CPU time / RSS): a 60,000-character streamed Markdown reply
24.2 s → 4.8 s; a 200 KB write (`pi -p`) 9.7 s → 0.1 s; memory for a 200 KB write in the TUI 316 MB → 105 MB; startup
unchanged; reading one 3000 px image 25 s → 0.3 s.

**`streaming-tool-arguments` changes behavior**: intermediate parsed snapshots of large tool arguments are updated less
often; raw deltas and final arguments stay complete. For an extension that needs a fresh parsed snapshot on every delta,
switch back to the original Pi with `pitch off`.

How the patches are maintained and changed: [PATCHES.md](PATCHES.md).

## Output and portability

The whole output directory is the build, not just `pi`. `pitch-build.json` records the sources, toolchain, entries,
actual package versions and commits, arguments and file hashes; `source_tree` is the Git tree of the Pi that was built,
`checks` lists the checks that actually ran, and `extensions_auto_load` marks the loading behavior.
`recipe/extensions.json` keeps the packages as declared, the resolved entries and the npm lock files found; no other
input settings.

`--compile-in-place` keeps the module identities of the build. The selected pi-loop and controlled extensions were
verified to still load automatically after the build directory and local entries moved away, and a diagnostic build
that forbids code from disk and code generated from strings verified that this is not a fallback to source. **That does
not make arbitrary extensions portable across machines.** Dynamic paths, native libraries and run-time assets need
handling one by one; Bun positions in-place compilation for local builds.

Unless it is set, the entry points `PI_PACKAGE_DIR` at the build directory, which carries the unbundled resource layout
Pi needs, so that version, themes and the like do not depend on the build tree. Full image, export, all built-in
extensions and arbitrary extensions with their own assets are not proven compatible.

A later build without pinned versions or refs may get different inputs. The lock files are recorded, but there is no
command to replay them, no full input snapshot and no byte-for-byte reproducibility. Official AOT built-ins and dynamic
loading differ from stock Pi; run-time code may be interpreted.

Before `build` ends it checks the output files against the manifest; that is a self-check of the build, not a release
signature. There is no Apple notarization and no complete redistribution compliance review; the licenses are kept, and
public redistribution must follow the third-party terms.

## Verify

```sh
go test -race ./...
go vet ./...

# a real build: default loading, extensions from disk, management commands, a local SSE write and more
PITCH_TEST_ARTIFACT="$PWD/dist/my-pi" go test -race ./...

# the pinned Pi work tree must have finished its offline build; package tests use local npm and Git fixtures
PITCH_TEST_PI=/absolute/build-work/pi go test -race ./...

# Pi's own features end to end (CLI, tools, codemode, MCP, sessions, skills/templates/extensions, RPC, TUI) with a local
# fake model: first on the original Pi, then on the build
bun bench/e2e/run.ts ~/.bun/bin/pi
bun bench/e2e/run.ts "$PWD/dist/my-pi/pi"
bun bench/e2e/run.ts bin/pitch pi        # the installed build, through pitch pi
```

Integration tests skip when their variables are not set. Performance records are in `docs/pi-perf/results/`. Failed
builds and experiments stay in `.work/` or the pitch cache.

## Thanks

Thanks to the developers of Pi and JavaScriptCore, and to [Bun](https://github.com/oven-sh/bun) and
[Pi-Bolt](https://github.com/opensec-git/Pi-Bolt).
