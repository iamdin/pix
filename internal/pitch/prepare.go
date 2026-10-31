package pitch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// preparedInfo describes a patched, built and tested Pi source tree (pitch-prepared.json in its root).
type preparedInfo struct {
	Pitch      string `json:"pitch"`
	Commit     string `json:"commit"`
	SourceTree string `json:"source_tree"`
	NPMLock    string `json:"npm_lock_sha256"`
	Node       string `json:"node"`
	NPM        string `json:"npm"`
}

const preparedFile = "pitch-prepared.json"

func hasPreparedPi() bool {
	_, err := runtimeData.Open("runtime-data/pi.tar.gz")
	return err == nil
}

// preparePi checks out the pinned Pi, applies local patches, installs dependencies, builds Pi and runs the patch tests.
// It needs git, Node 24+ and npm; releases run it once (Prepare) so `pitch build` does not.
func preparePi(r runner, cache string, pins Sources, patches []Patch, bun, stage, source string, log io.Writer, step func(string)) (info preparedInfo, err error) {
	for _, tool := range []string{"git", "node", "npm"} {
		if _, err := exec.LookPath(tool); err != nil {
			return info, fmt.Errorf("preparing Pi requires %s in PATH", tool)
		}
	}
	info = preparedInfo{Pitch: Version, Commit: pins.Pi.Commit}
	if info.Node, err = r.output("", "node", "--version"); err != nil {
		return info, err
	}
	var major int
	if _, err = fmt.Sscanf(info.Node, "v%d.", &major); err != nil || major < 24 {
		return info, fmt.Errorf("preparing Pi requires Node 24 or newer; found %s", info.Node)
	}
	if info.NPM, err = r.output("", "npm", "--version"); err != nil {
		return info, err
	}
	step("Get Pi source")
	pristine, err := checkout(r, cache, pins.Pi, log)
	if err != nil {
		return info, err
	}
	if err = r.command("", log, "git", "clone", "--no-local", "--no-hardlinks", pristine, source); err != nil {
		return info, err
	}
	sourceCommit, err := r.output(source, "git", "rev-parse", "HEAD")
	if err != nil {
		return info, err
	}
	if sourceCommit != pins.Pi.Commit {
		return info, fmt.Errorf("cloned source does not match the pinned commit")
	}
	if err = applyPatches(r, source, stage, patches, log); err != nil {
		return info, err
	}
	if info.SourceTree, err = r.output(source, "git", "write-tree"); err != nil {
		return info, err
	}
	var catalog struct{ Revision string }
	bytes, err := os.ReadFile(filepath.Join(source, "nix/model-catalog.json"))
	if err != nil {
		return info, err
	}
	if err = json.Unmarshal(bytes, &catalog); err != nil {
		return info, err
	}
	if catalog.Revision != pins.ModelCatalog {
		return info, fmt.Errorf("unexpected upstream model catalog revision")
	}
	catalogPath := filepath.Join(cache, "models-"+pins.ModelCatalog+".json")
	if err = download(r.ctx, "https://pi.dev/api/models/revisions/"+pins.ModelCatalog+"?types=chat,image,classifier", catalogPath, strings.TrimPrefix(pins.ModelCatalog, "sha256-")); err != nil {
		return info, err
	}
	step("Install dependencies and build Pi")
	for _, command := range [][]string{{"npm", "ci", "--ignore-scripts", "--no-audit", "--no-fund"}, {"node", "packages/ai/scripts/hydrate-model-catalog.ts", catalogPath}, {"npm", "run", "build:offline"}} {
		if err = r.command(source, log, command[0], command[1:]...); err != nil {
			return info, err
		}
	}
	if err = verifySource(r, source, info.SourceTree); err != nil {
		return info, err
	}
	// Local patches may change or take out pitch's patches (bench/build-variants.ts reverts them), and their tests with them.
	if len(patches) > 0 {
		step("Skip the patch tests (local patches)")
	} else {
		step("Test patches")
		for _, test := range piTests {
			name := test.Args[0]
			if name == "bun" {
				name = bun
			}
			if err = r.command(filepath.Join(source, test.Dir), log, name, test.Args[1:]...); err != nil {
				return info, fmt.Errorf("patch tests: %w", err)
			}
		}
	}
	info.NPMLock, err = hashFile(filepath.Join(source, "package-lock.json"))
	return info, err
}

// unpackPreparedPi extracts the Pi that the release embeds (Prepare) into source.
func unpackPreparedPi(r runner, pins Sources, stage, source string, log io.Writer) (info preparedInfo, err error) {
	archive := filepath.Join(stage, "pi.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		return info, err
	}
	err = writeRuntimeData("pi.tar.gz", f)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return info, err
	}
	if err = os.Mkdir(source, 0700); err != nil {
		return info, err
	}
	if err = r.command("", log, "tar", "-xzf", archive, "-C", source); err != nil {
		return info, err
	}
	if err = os.Remove(archive); err != nil {
		return info, err
	}
	data, err := os.ReadFile(filepath.Join(source, preparedFile))
	if err != nil {
		return info, err
	}
	if err = json.Unmarshal(data, &info); err != nil {
		return info, err
	}
	if info.Pitch != Version || info.Commit != pins.Pi.Commit {
		return info, fmt.Errorf("the embedded Pi was prepared for pitch %s from %s, not this pitch", info.Pitch, info.Commit)
	}
	return info, nil
}

// Prepare writes Pi, patched, built, tested and without dev dependencies, to a .tar.gz that
// releases embed as runtime-data/pi.tar.gz.
func Prepare(ctx context.Context, cache, out string, stdout io.Writer) (err error) {
	pins, err := sourcePins()
	if err != nil {
		return err
	}
	if out, err = filepath.Abs(out); err != nil {
		return err
	}
	if err = os.MkdirAll(cache, 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(cache, "prepare-")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = fmt.Errorf("%w; work retained at %s", err, stage)
		} else {
			err = os.RemoveAll(stage)
		}
	}()
	home := filepath.Join(stage, "home")
	if err = os.Mkdir(home, 0700); err != nil {
		return err
	}
	r := runner{ctx, buildEnvironment(home, cache)}
	log, err := os.Create(filepath.Join(stage, "build.log"))
	if err != nil {
		return err
	}
	defer log.Close()
	fmt.Fprintf(stdout, "Preparing Pi %s for pitch %s\nLog: %s\n", strings.TrimPrefix(pins.Pi.Tag, "v"), Version, log.Name())
	bun, err := getRuntime(r, cache, pins.Bun)
	if err != nil {
		return err
	}
	dataDir := filepath.Join(stage, "recipe")
	if err = materializeAssets(dataDir); err != nil {
		return err
	}
	patches, err := preparePatches(dataDir, nil)
	if err != nil {
		return err
	}
	source := filepath.Join(stage, "pi")
	info, err := preparePi(r, cache, pins, patches, bun, stage, source, log, func(s string) { fmt.Fprintln(stdout, s) })
	if err != nil {
		return err
	}
	// Bun's AOT compile type-checks, so the @types dev dependencies stay.
	types, saved := filepath.Join(source, "node_modules/@types"), filepath.Join(stage, "types")
	if err = os.Rename(types, saved); err != nil {
		return err
	}
	if err = r.command(source, log, "npm", "prune", "--omit=dev", "--ignore-scripts", "--no-audit", "--no-fund"); err != nil {
		return err
	}
	if err = os.RemoveAll(types); err != nil {
		return err
	}
	if err = os.Rename(saved, types); err != nil {
		return err
	}
	if err = writeJSON(filepath.Join(source, preparedFile), info); err != nil {
		return err
	}
	if err = r.command("", log, "tar", "-czf", out, "--exclude", "./.git", "-C", source, "."); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Prepared %s\n", out)
	return nil
}
