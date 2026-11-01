package pix

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Manifest struct {
	Schema             int               `json:"schema"`
	Pix                string            `json:"pix"`
	BuilderSHA256      string            `json:"builder_sha256"`
	Sources            Sources           `json:"sources"`
	Extensions         []Extension       `json:"extensions"`
	ExtensionsAutoLoad bool              `json:"extensions_auto_load,omitempty"`
	SourceTree         string            `json:"source_tree"`
	NPMLock            string            `json:"npm_lock_sha256"`
	Node               string            `json:"node"`
	NPM                string            `json:"npm"`
	BuiltAt            string            `json:"built_at"`
	CompilerArgs       []string          `json:"compiler_args"`
	Checks             []string          `json:"checks"`
	Limitations        []string          `json:"limitations"`
	Files              map[string]string `json:"files"`
	// Work directory in the pix cache; compiled extensions load files from it at run time.
	Work string `json:"work"`
}

func Build(ctx context.Context, opts Options, stdout io.Writer) (err error) {
	pins, err := sourcePins()
	if err != nil {
		return err
	}
	if _, err := pins.Bun.host(); err != nil {
		return err
	}
	if _, err := exec.LookPath("codesign"); err != nil && runtime.GOOS == "darwin" {
		return fmt.Errorf("build requires codesign in PATH")
	}
	if opts.Pi != "" {
		if opts.Pi, err = filepath.Abs(opts.Pi); err != nil {
			return err
		}
		if _, err = os.Stat(opts.Pi); err != nil {
			return err
		}
	}
	if opts.Settings != "" {
		opts.Settings, err = filepath.Abs(opts.Settings)
		if err != nil {
			return err
		}
		data, readErr := os.ReadFile(opts.Settings)
		if readErr != nil {
			return readErr
		}
		var config map[string]json.RawMessage
		if err := json.Unmarshal(data, &config); err != nil {
			return err
		}
		var packages []json.RawMessage
		if raw := config["packages"]; len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &packages) != nil {
			return fmt.Errorf("settings input must contain a packages array")
		}
	}
	opts.Out, err = filepath.Abs(opts.Out)
	if err != nil {
		return err
	}
	opts.Cache, err = filepath.Abs(opts.Cache)
	if err != nil {
		return err
	}
	if exists(opts.Out) {
		return fmt.Errorf("output already exists; not overwritten: %s", opts.Out)
	}
	if err = os.MkdirAll(opts.Cache, 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(opts.Cache, "build-")
	if err != nil {
		return err
	}
	start, prog, current := time.Now(), newProgress(stdout), ""
	var extensions []Extension
	defer func() {
		if err != nil {
			prog.end()
			// Keep the log and the feedback, not the copies of Pi and the packages.
			for _, dir := range []string{"pi", "extension-packages", "agent", "home", "smoke-home"} {
				_ = os.RemoveAll(filepath.Join(stage, dir))
			}
			if ctx.Err() != nil { // the user stopped it: nothing to report
				err = fmt.Errorf("stopped; log at %s", filepath.Join(stage, "build.log"))
				return
			}
			err = failure(err, stage, current, time.Since(start), extensions)
		}
	}()
	home := filepath.Join(stage, "home")
	if err = os.Mkdir(home, 0700); err != nil {
		return err
	}
	r := runner{ctx, buildEnvironment(home, opts.Cache)}
	log, err := os.OpenFile(filepath.Join(stage, "build.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	step := func(s string) {
		current = s
		prog.step(s)
	}
	fmt.Fprintf(stdout, "Building Pi %s with pix %s, a few minutes\nLog: %s\n", strings.TrimPrefix(pins.Pi.Tag, "v"), Version, filepath.Join(stage, "build.log"))
	bun, err := getRuntime(r, opts.Cache, pins.Bun)
	if err != nil {
		return err
	}
	if err = verifySignature(r, log, bun); err != nil {
		return err
	}
	dataDir := filepath.Join(stage, "recipe")
	if err = materializeAssets(dataDir); err != nil {
		return err
	}
	source := filepath.Join(stage, "pi")
	step("Unpack the patched Pi")
	info, err := unpackPi(r, pins, opts.Pi, source, log)
	if err != nil {
		return err
	}
	step("Fetch extensions")
	extensions, err = resolveExtensions(r, bun, source, stage, opts.Extensions, opts.Settings, opts.Installed, log)
	if err != nil {
		return err
	}
	prog.end()
	for _, extension := range extensions {
		fmt.Fprintf(stdout, "      %s\n", extension.Name)
	}
	if err = os.MkdirAll(filepath.Dir(opts.Out), 0755); err != nil {
		return err
	}
	if err = os.Mkdir(opts.Out, 0755); err != nil {
		return fmt.Errorf("cannot reserve output (not overwritten): %w", err)
	}
	marker := filepath.Join(opts.Out, ".pix-incomplete")
	if err = os.WriteFile(marker, []byte("Incomplete build; see "+stage+"/build.log\n"), 0600); err != nil {
		return err
	}
	entry := filepath.Join(stage, "entry.ts")
	if err = os.WriteFile(entry, []byte(entrySource(source, extensions)), 0600); err != nil {
		return err
	}
	config, err := compilerConfig(source, entry)
	if err != nil {
		return err
	}
	tsconfig := filepath.Join(stage, "tsconfig.json")
	if err = writeJSON(tsconfig, config); err != nil {
		return err
	}
	executable := filepath.Join(opts.Out, "pi")
	args := []string{"build", "--compile", "--aot", "--format=esm", "--aot-allow-code-from-disk", "--no-compile-autoload-dotenv", "--no-compile-autoload-bunfig", "--tsconfig-override=" + tsconfig, "--metafile=" + filepath.Join(stage, "metafile.json"), entry, filepath.Join(source, "packages/coding-agent/src/utils/image-resize-worker.ts"), filepath.Join(source, "packages/coding-agent/src/extensions/codemode/worker.ts"), filepath.Join(source, "packages/codemode/src/runtime/worker.ts"), "--outfile", executable}
	args = append(args, "--compile-in-place", "--asset="+filepath.Join(source, "packages/coding-agent/package.json"))
	for _, extension := range extensions {
		args = append(args, extension.Entry)
	}
	if exists(filepath.Join(stage, "extensions.json")) {
		data, readErr := os.ReadFile(filepath.Join(stage, "extensions.json"))
		if readErr != nil {
			return readErr
		}
		var resolution struct{ Assets []string }
		if err = json.Unmarshal(data, &resolution); err != nil {
			return err
		}
		for _, asset := range resolution.Assets {
			args = append(args, "--asset="+asset)
		}
	}
	step("Compile to machine code (the longest step)")
	if err = compile(ctx, r, opts.Timeout, prog, source, log, bun, args...); err != nil {
		return err
	}
	for _, extension := range extensions {
		if err = requireHash(extension.Entry, extension.SHA256); err != nil {
			return err
		}
	}
	if err = packageAssets(source, opts.Out); err != nil {
		return err
	}
	// Every license that comes with pix's data goes with the build.
	licenses, err := filepath.Glob(filepath.Join(dataDir, "LICENSE.*"))
	if err != nil {
		return err
	}
	for _, license := range licenses {
		if err = copyFile(license, filepath.Join(opts.Out, filepath.Base(license)), 0644); err != nil {
			return err
		}
	}
	if err = os.Mkdir(filepath.Join(opts.Out, "recipe"), 0755); err != nil {
		return err
	}
	for name, path := range map[string]string{"entry.ts": entry, "tsconfig.json": tsconfig, "sources.lock.json": filepath.Join(dataDir, "sources.lock.json"), "metafile.json": filepath.Join(stage, "metafile.json")} {
		if err = copyFile(path, filepath.Join(opts.Out, "recipe", name), 0644); err != nil {
			return err
		}
	}
	if len(opts.Extensions) > 0 || opts.Settings != "" {
		for _, file := range []string{"extensions.json", "recipe/resolve-extensions.mjs"} {
			if err = copyFile(filepath.Join(stage, file), filepath.Join(opts.Out, "recipe", filepath.Base(file)), 0644); err != nil {
				return err
			}
		}
	}
	step("Check the build")
	if err = verifySignature(r, log, executable); err != nil {
		return err
	}
	if err = smoke(ctx, executable, stage, info.Pi.Tag); err != nil {
		return err
	}
	if err = copyFile(filepath.Join(stage, "build.log"), filepath.Join(opts.Out, "build.log"), 0600); err != nil {
		return err
	}
	if err = os.Remove(marker); err != nil {
		return err
	}
	hashes, err := fileHashes(opts.Out)
	if err != nil {
		return err
	}
	builder, err := os.Executable()
	if err != nil {
		return err
	}
	builderHash, err := hashFile(builder)
	if err != nil {
		return err
	}
	checks := []string{"runtime archive and binary SHA-256", "runtime revision and macOS code signature", "Pi commit and source tree, when Pi was prepared"}
	if info.Tested {
		checks = append(checks, "patch unit tests, when Pi was prepared")
	}
	checks = append(checks, "artifact macOS code signature", "version", "help", "isolated RPC get_state")
	pins.Pi = info.Pi
	manifest := Manifest{Schema: 1, Pix: Version, BuilderSHA256: builderHash, Sources: pins, Extensions: extensions, ExtensionsAutoLoad: true, SourceTree: info.SourceTree, NPMLock: info.NPMLock, Node: info.Node, NPM: info.NPM, BuiltAt: time.Now().UTC().Format(time.RFC3339), CompilerArgs: args, Work: stage, Checks: checks, Limitations: []string{"With streaming-tool-arguments, intermediate argument snapshots of large tool calls may lag; raw deltas and final arguments remain complete.", "Experimental Bun AOT; built-in mutation and module-loader behavior differ from stock Pi.", "Extensions on disk load as in Pi, except those of compiled-in packages; not a sandbox.", "Selected extensions auto-load, including during build smoke checks; use trusted packages only. No extension permission sandbox.", "Code loaded from disk or generated at runtime is interpreted, not AOT. In-place module identities and cross-machine portability require validation.", "No performance, full provider, TUI, image or multi-day stability claim.", "Package sources use pinned Pi resolution in isolated storage; npm install scripts are disabled. Local dependencies, dynamic imports and native/runtime assets are not made hermetic by this build."}, Files: hashes}
	if err = writeJSON(filepath.Join(opts.Out, "pix-build.json"), manifest); err != nil {
		return err
	}
	if err = Verify(opts.Out); err != nil {
		return err
	}
	// Only compiled extensions read from the work directory at run time, from their packages; Pi's copy is not used.
	drop := filepath.Join(stage, "pi")
	if len(extensions) == 0 {
		drop = stage
	}
	if err = os.RemoveAll(drop); err != nil {
		return err
	}
	prog.end()
	fmt.Fprintf(stdout, "Built in %s\n", time.Since(start).Round(time.Second))
	return nil
}

func packageAssets(source, out string) error {
	root := filepath.Join(source, "packages/coding-agent")
	for _, name := range []string{"package.json", "README.md", "CHANGELOG.md"} {
		if err := copyFile(filepath.Join(root, name), filepath.Join(out, name), 0644); err != nil {
			return err
		}
	}
	for _, name := range []string{"docs", "examples", "schemas"} {
		if err := copyTree(filepath.Join(root, name), filepath.Join(out, name)); err != nil {
			return err
		}
	}
	for dest, src := range map[string]string{"theme": "src/modes/interactive/theme", "assets": "src/modes/interactive/assets", "export-html": "src/core/export-html"} {
		// Copy only runtime assets, not the TypeScript implementation beside them.
		if err := filepath.WalkDir(filepath.Join(root, src), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(filepath.Join(root, src), path)
			if err != nil {
				return err
			}
			target := filepath.Join(out, dest, rel)
			if d.IsDir() {
				return os.MkdirAll(target, 0755)
			}
			if strings.HasSuffix(path, ".ts") {
				return nil
			}
			if !d.Type().IsRegular() {
				return fmt.Errorf("non-regular asset: %s", path)
			}
			return copyFile(path, target, 0644)
		}); err != nil {
			return err
		}
	}
	// In-place module URLs use Pi's unbundled resource layout, relative to PI_PACKAGE_DIR.
	for dest, src := range map[string]string{"dist/modes/interactive/theme": "theme", "dist/modes/interactive/assets": "assets", "dist/core/export-html": "export-html"} {
		if err := copyTree(filepath.Join(out, src), filepath.Join(out, dest)); err != nil {
			return err
		}
	}
	// Native clipboard and modifier-key helpers, next to the executable as in Pi's own binaries.
	native := "native/" + runtime.GOOS + "/prebuilds/" + runtime.GOOS + "-" + nodeArch(runtime.GOARCH)
	if err := copyTree(filepath.Join(source, "packages/tui", native), filepath.Join(out, native)); err != nil {
		return err
	}
	return copyFile(filepath.Join(source, "node_modules/@silvia-odwyer/photon-node/photon_rs_bg.wasm"), filepath.Join(out, "photon_rs_bg.wasm"), 0644)
}

// verifySignature checks a macOS code signature; Linux executables are not signed.
func verifySignature(r runner, log io.Writer, path string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	return r.command("", log, "codesign", "--verify", "--strict", path)
}

// nodeArch is Node's name (process.arch) for a GOARCH.
func nodeArch(goarch string) string {
	if goarch == "amd64" {
		return "x64"
	}
	return goarch
}
