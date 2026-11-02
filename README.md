# pix

Pi, built ahead of time with official Bun AOT and performance patches. Anyone who can use Pi can use pix: it reads
Pi's settings, packages and sessions (`~/.pi`) and loads extensions as Pi does, and compiles your extensions in for speed by
itself, in the background. **Currently 0.0.5, for macOS ARM64, Linux x64 and Linux ARM64 (glibc). Still an experimental toolchain.**

## Install

```sh
curl -fsSL https://github.com/iamdin/pix/releases/latest/download/install.sh | sh
# or
npm i -g usepix
```

The npm package is a launcher: its first run installs pix with the same script, into `~/.pix/bin`.

Installs `~/.local/bin/pix` (about 65 MB, with Bun inside for `pix compile`; change the directory with
`PIX_INSTALL_DIR`), then the Pi that the release compiled for your platform (about 55 MB) into `~/.pix`, both checked
against SHA-256 sums. Nothing is built on your machine and no Node is needed; the original Pi is not changed. Installing
packages uses npm or git, as with Pi.

From source: `go build -trimpath -o bin/pix ./cmd/pix` (Go 1.24+, and the Bun archive for your platform in
`internal/pix/runtime-data/<goos>-<goarch>/bun.zip`, which is not in git). Such a development build has no release
to download from: run `pix self prepare /abs/pi.tar.gz` first (needs Git, Node 24+ and npm 11+), then `pix compile --pi
/abs/pi.tar.gz --out DIR`. `sh scripts/fetch-bun.sh` downloads the Bun archives. `bun run release patch` (or minor,
major, X.Y.Z) releases: it sets the version, tags it, and GitHub Actions builds, checks and publishes the release
(`.github/workflows/release.yml`).

## Use

```sh
pix                    # Pi: every argument and command is Pi's (pix -c, pix -p "...", pix install npm:x)
pix compile            # compile the packages in ~/.pi/agent/settings.json in now (pix also does it by itself)
pix compile --none     # the release's Pi, nothing compiled in, and no compiling in the background
pix self status        # Pi version, compiled-in extensions, the compile in the background, the latest release
pix self uninstall     # remove ~/.pix and its builds; Pi's settings and sessions stay
pix self feedback FILE # send a failed compile's feedback (it names FILE) as a GitHub issue, after you confirm
```

- **Your packages are compiled in by themselves.** When the packages in Pi settings are not those compiled into the
  current build (after `pix install`, `remove` or `update`, after an update of pix, or the first time), pix compiles
  them into Pi in the background at low priority and says so; a run never waits for it. Until it is done they load
  from disk, as in Pi (without a JIT they run interpreted), and the next run after it uses the new build and says so.
  A failed compile is shown once in Pi with its log (`~/.pix/compile.log`) and not tried again until the packages or
  pix change. A compiled-in package's extensions are not loaded again from disk.
- `pix compile` compiles the packages as installed (`pix install`) together with Pi, with the Bun inside pix and Pi's
  prepared sources from the release (about 45 MB, downloaded then and removed afterwards); it runs no npm. It links the
  packages' files into the build with hard links, which take no space; the build is about 200 MB. Only the build in use
  is kept.
- `pix install/remove/update` work as in Pi. `pix update` updates pix and its Pi (`--extensions` updates packages,
  `--all` both).
- **Updates are yours to run, as in Pi.** A run checks for a new pix release in the background at most once an hour,
  and Pi shows `pix X is available: run pix update`, with a link to the release, when one is out (or, when an
  earlier release is the latest again, that `pix update` goes back to it); `pix self status` shows the last check (`PI_SKIP_VERSION_CHECK=1` or `PI_OFFLINE=1` turn
  the check off, as for Pi). `pix update` installs it, and your packages are compiled into the new Pi in the background. To go back to an earlier release
  (0.0.4 or later): `curl -fsSL https://github.com/iamdin/pix/releases/latest/download/install.sh | PIX_VERSION=0.0.4 sh`.
- A new build is switched to only after it passes its checks. The previous build is kept for sessions still running it;
  older ones are deleted along with their build directories in the cache.
- `compile` shows each step and how long it took (the current one counts up in a terminal; elsewhere a long compile adds
  a line every 5 minutes) and stops compiling after 30 minutes (`--timeout`, `0` for no limit). An extension with a very
  large function can compile for a long time; leave one out with `{"source": "...", "extensions": []}` in the settings
  to find it, or use `pix compile --none` to go back to the release's Pi.
- A failed compile writes feedback for a GitHub issue next to its log, with your home directory replaced by `~`, and
  sends nothing. `pix self feedback FILE` shows it and asks before sending it with the GitHub CLI when `gh` is logged
  in; otherwise it prints a prefilled link to submit in the browser. `-y` sends without asking, for an agent that has
  shown the feedback to you and has your agreement.

## Standalone builds

```sh
pix compile --out ./dist/my-pi
./dist/my-pi/pi

# a new settings file, in the format of the packages in Pi's settings
pix compile --settings ./pix-settings.json --out ./dist/my-pi-ext
```

Example `pix-settings.json`:

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

Sources can also be given directly:

```sh
./bin/pix compile --extension npm:@getpie/pi-loop --out ./dist/pi-loop
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
5. The generated entry passes the compiled paths to Pi's own `-e` and has Pi skip the same packages' extensions on
   disk. **Compiled-in extensions load automatically; there is nothing to give at run time.**

The Go CLI orchestrates; a small JS bridge calls this Pi's package manager instead of reimplementing its npm and Git
resolution. Local sources are made absolute against the settings directory with the pinned Pi's internal resolver, so
upgrading Pi must run the resolution regression tests.

## Loading and safety

- Compiled-in extensions run, top-level module code and factories included; the help and RPC smoke checks during the
  build may run them too. **Build trusted packages only. The isolated HOME and directories are not a sandbox.**
- Other extensions (user, project, settings packages, `-e`) load from disk as in Pi, interpreted rather than AOT code.
- No `builtin:pix-NAME`, no new extension URI or ID protocol. Extension tool and command names are not rewritten;
  conflicts are Pi's to handle.
- Skills, prompts, themes, models, credentials and sessions come from your normal configuration, as do the skills,
  prompts and themes of packages.
- npm lifecycle scripts are disabled and user or global npm configuration is not read. Packages that need install
  scripts or private registry credentials may not build; compile-time code such as Bun macros may still run.
- Pi's self-update is `pix update`, which installs the latest pix; Pi's notice of a newer Pi is off, as pix follows Pi
  releases.

## Pinned toolchain and optimizations

| Component | Pinned |
|---|---|
| Pi | v1.4.0 with pix's patches: [getpie-dev/pi](https://github.com/getpie-dev/pi) `pix-v1.4.0`, f9731d41a29da59e2893622d7189ce85840cf137 |
| Bun | 1.4.4-canary.1, 3f3a9ff87cdccf3d32d7ec6b648c664eddaae840 |
| WebKit/JSC | 4d45755fed4aeef19e95beb90d3d0c884a2812bd |

There is no choice of Pi or Bun version. Pi follows upstream release tags only, and pix's patches are commits on a
fork (see [PATCHES.md](PATCHES.md)). Bun AOT exists only in canary builds, so Bun is pinned to one official canary build
and moves when the maintainers upgrade it. Each platform's pix embeds that platform's pinned Bun archive and the SHA-256
sums of the release's compiled Pi and prepared sources, and checks the archive and binary hashes and the revision, and on
macOS the code signature.
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

Pi v1.4.0 with official AOT, stock → pix (Apple M4, CPU time / RSS): a 60,000-character streamed Markdown reply
22.9 s → 4.3 s; a 200 KB write (`pi -p`) 9.6 s → 0.1 s; memory for a 200 KB write in the TUI 320 MB → 115 MB; startup
unchanged; reading one 3000 px image 25 s → 0.3 s.

**`streaming-tool-arguments` changes behavior**: intermediate parsed snapshots of large tool arguments are updated less
often; raw deltas and final arguments stay complete. For an extension that needs a fresh parsed snapshot on every delta,
use the original Pi.

How the patches are maintained and changed: [PATCHES.md](PATCHES.md).

## Output and portability

The whole output directory is the build, not just `pi`. `pix-build.json` records the sources, toolchain, entries,
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
PIX_TEST_ARTIFACT="$PWD/dist/my-pi" go test -race ./...

# the pinned Pi work tree must have finished its offline build; package tests use local npm and Git fixtures
PIX_TEST_PI=/absolute/build-work/pi go test -race ./...

# Pi's own features end to end (CLI, tools, codemode, MCP, sessions, skills/templates/extensions, RPC, TUI) with a local
# fake model: first on the original Pi, then on the build
bun bench/e2e/run.ts ~/.bun/bin/pi
bun bench/e2e/run.ts "$PWD/dist/my-pi/pi"
bun bench/e2e/run.ts bin/pix            # the installed build, through pix
```

Integration tests skip when their variables are not set. Performance records are in `docs/pi-perf/results/`. Failed
builds and experiments stay in `.work/` or the pix cache.

## Thanks

Thanks to the developers of Pi and JavaScriptCore, and to [Bun](https://github.com/oven-sh/bun) and
[Pi-Bolt](https://github.com/opensec-git/Pi-Bolt).
