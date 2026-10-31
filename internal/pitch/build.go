package pitch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

type Manifest struct {
	Schema             int               `json:"schema"`
	Pitch              string            `json:"pitch"`
	BuilderSHA256      string            `json:"builder_sha256"`
	Sources            Sources           `json:"sources"`
	Features           []Feature         `json:"features"`
	Patches            []Patch           `json:"patches,omitempty"`
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
}

func Build(ctx context.Context, opts Options, stdout io.Writer) (err error) {
	pins, err := sourcePins()
	if err != nil {
		return err
	}
	if runtime.GOOS+"/"+runtime.GOARCH != pins.Bun.Platform {
		return fmt.Errorf("this alpha supports only %s", pins.Bun.Platform)
	}
	if opts.Features == "" {
		opts.Features = defaultFeatures
	}
	chosen, err := selectFeatures(opts.Features)
	if err != nil {
		return err
	}
	local, err := readLocalPatches(opts.Patches)
	if err != nil {
		return err
	}
	for _, tool := range []string{"git", "node", "npm", "codesign"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("build requires %s in PATH", tool)
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
	defer func() {
		if err != nil {
			err = fmt.Errorf("%w; work retained at %s", err, stage)
		}
	}()
	home := filepath.Join(stage, "home")
	if err = os.Mkdir(home, 0700); err != nil {
		return err
	}
	r := runner{ctx, buildEnvironment(home, opts.Cache)}
	node, err := r.output("", "node", "--version")
	if err != nil {
		return err
	}
	var major int
	if _, err = fmt.Sscanf(node, "v%d.", &major); err != nil || major < 24 {
		return fmt.Errorf("build requires Node 24 or newer; found %s", node)
	}
	npm, err := r.output("", "npm", "--version")
	if err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(stage, "build.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	fmt.Fprintf(stdout, "Experimental official AOT.\nWork: %s\n", stage)
	fmt.Fprintf(stdout, "Verify compiler %s and Pi %s\n", pins.Bun.Revision, pins.Pi.Tag)
	bun, err := getRuntime(r, opts.Cache, pins.Bun)
	if err != nil {
		return err
	}
	if err = r.command("", log, "codesign", "--verify", "--strict", bun); err != nil {
		return err
	}
	if err = verifyTag(r, pins.Pi); err != nil {
		return err
	}
	pristine, err := checkout(r, opts.Cache, pins.Pi, log)
	if err != nil {
		return err
	}
	source := filepath.Join(stage, "pi")
	if err = r.command("", log, "git", "clone", "--no-local", "--no-hardlinks", pristine, source); err != nil {
		return err
	}
	sourceCommit, err := r.output(source, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if sourceCommit != pins.Pi.Commit {
		return fmt.Errorf("cloned source does not match the pinned commit")
	}
	dataDir := filepath.Join(stage, "recipe")
	if err = materializeAssets(dataDir); err != nil {
		return err
	}
	patches, err := preparePatches(dataDir, chosen, local)
	if err != nil {
		return err
	}
	if err = applyPatches(r, source, stage, patches, log); err != nil {
		return err
	}
	tree, err := r.output(source, "git", "write-tree")
	if err != nil {
		return err
	}
	var catalog struct{ Revision string }
	bytes, err := os.ReadFile(filepath.Join(source, "nix/model-catalog.json"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(bytes, &catalog); err != nil {
		return err
	}
	if catalog.Revision != pins.ModelCatalog {
		return fmt.Errorf("unexpected upstream model catalog revision")
	}
	catalogPath := filepath.Join(opts.Cache, "models-"+pins.ModelCatalog+".json")
	if err = download(ctx, "https://pi.dev/api/models/revisions/"+pins.ModelCatalog+"?types=chat,image,classifier", catalogPath, strings.TrimPrefix(pins.ModelCatalog, "sha256-")); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Install locked dependencies and build Pi offline")
	for _, command := range [][]string{{"npm", "ci", "--ignore-scripts", "--no-audit", "--no-fund"}, {"node", "packages/ai/scripts/hydrate-model-catalog.ts", catalogPath}, {"npm", "run", "build:offline"}} {
		if err = r.command(source, log, command[0], command[1:]...); err != nil {
			return err
		}
	}
	if err = verifySource(r, source, tree); err != nil {
		return err
	}
	for _, feature := range chosen {
		for _, test := range feature.Tests {
			name := test.Args[0]
			if name == "bun" {
				name = bun
			}
			if err = r.command(filepath.Join(source, test.Dir), log, name, test.Args[1:]...); err != nil {
				return fmt.Errorf("%s tests: %w", feature.ID, err)
			}
		}
	}
	extensions, err := resolveExtensions(r, source, stage, opts.Extensions, opts.Settings, log)
	if err != nil {
		return err
	}
	for _, extension := range extensions {
		fmt.Fprintf(stdout, "Compile and auto-load %s: %s\n", extension.Source, extension.Entry)
	}
	if err = os.MkdirAll(filepath.Dir(opts.Out), 0755); err != nil {
		return err
	}
	if err = os.Mkdir(opts.Out, 0755); err != nil {
		return fmt.Errorf("cannot reserve output (not overwritten): %w", err)
	}
	marker := filepath.Join(opts.Out, ".pitch-incomplete")
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
	fmt.Fprintln(stdout, "Compile Pi and selected extensions to machine code")
	if err = r.command(source, log, bun, args...); err != nil {
		return err
	}
	if err = verifySource(r, source, tree); err != nil {
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
	for _, name := range []string{"LICENSE.Pi", "LICENSE.Bun"} {
		if err = copyFile(filepath.Join(dataDir, name), filepath.Join(opts.Out, name), 0644); err != nil {
			return err
		}
	}
	// Every patch but native-image-resize follows Pi-Bolt (MIT).
	if slices.ContainsFunc(chosen, func(f Feature) bool { return f.ID != "native-image-resize" }) {
		if err = copyFile(filepath.Join(dataDir, "LICENSE.Pi-Bolt"), filepath.Join(opts.Out, "LICENSE.Pi-Bolt"), 0644); err != nil {
			return err
		}
	}
	if err = os.Mkdir(filepath.Join(opts.Out, "recipe"), 0755); err != nil {
		return err
	}
	for _, patch := range patches {
		if err = requireHash(filepath.Join(stage, patch.File), patch.SHA256); err != nil {
			return err
		}
		if err = copyFile(filepath.Join(stage, patch.File), filepath.Join(opts.Out, patch.File), 0644); err != nil {
			return err
		}
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
	fmt.Fprintln(stdout, "Verify signature, version, help and isolated RPC startup")
	if err = r.command("", log, "codesign", "--verify", "--strict", executable); err != nil {
		return err
	}
	if err = smoke(ctx, executable, stage, pins.Pi.Tag); err != nil {
		return err
	}
	if err = copyFile(filepath.Join(stage, "build.log"), filepath.Join(opts.Out, "build.log"), 0600); err != nil {
		return err
	}
	lock, err := hashFile(filepath.Join(source, "package-lock.json"))
	if err != nil {
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
	manifest := Manifest{Schema: 1, Pitch: Version, BuilderSHA256: builderHash, Sources: pins, Features: chosen, Patches: patches, Extensions: extensions, ExtensionsAutoLoad: true, SourceTree: tree, NPMLock: lock, Node: node, NPM: npm, BuiltAt: time.Now().UTC().Format(time.RFC3339), CompilerArgs: args, Checks: []string{"runtime archive and binary SHA-256", "runtime revision and code signature", "release tag and source tree", "selected feature unit tests", "artifact code signature", "version", "help", "isolated RPC get_state"}, Limitations: []string{"Local patches can execute code during build, tests and smoke; isolation is not a sandbox. Patch hashes record inputs, not trust or semantic equivalence.", "With streaming-tool-arguments, intermediate argument snapshots of large tool calls may lag; raw deltas and final arguments remain complete.", "Experimental Bun AOT; built-in mutation and module-loader behavior differ from stock Pi.", "Pi's built-in extensions (llama.cpp, codemode, tool-search, mcp) always load; user, project and settings extensions on disk do not; not a sandbox.", "Selected extensions auto-load, including during build smoke checks; use trusted packages only. No extension permission sandbox.", "Code loaded from disk or generated at runtime is interpreted, not AOT. In-place module identities and cross-machine portability require validation.", "No performance, full provider, TUI, image or multi-day stability claim.", "Package sources use pinned Pi resolution in isolated storage; npm install scripts are disabled. Local dependencies, dynamic imports and native/runtime assets are not made hermetic by this build."}, Files: hashes}
	if err = writeJSON(filepath.Join(opts.Out, "pitch-build.json"), manifest); err != nil {
		return err
	}
	if err = Verify(opts.Out); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Built experimental Pi: %s\n", executable)
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
	native := "native/darwin/prebuilds/darwin-arm64"
	if err := copyTree(filepath.Join(source, "packages/tui", native), filepath.Join(out, native)); err != nil {
		return err
	}
	return copyFile(filepath.Join(source, "node_modules/@silvia-odwyer/photon-node/photon_rs_bg.wasm"), filepath.Join(out, "photon_rs_bg.wasm"), 0644)
}
