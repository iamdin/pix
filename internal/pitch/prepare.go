package pitch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// preparedInfo describes a built Pi source tree (pitch-prepared.json in its root).
type preparedInfo struct {
	Pitch      string `json:"pitch"`
	Pi         Source `json:"pi"`
	Tested     bool   `json:"tested"`
	SourceTree string `json:"source_tree"`
	NPMLock    string `json:"npm_lock_sha256"`
	Node       string `json:"node"`
	NPM        string `json:"npm"`
}

const preparedFile = "pitch-prepared.json"

// Prepare writes Pi, built and without dev dependencies, to a .tar.gz for `pitch build`: releases embed it as
// runtime-data/pi.tar.gz, and `pitch build --pi` takes one. It needs git, Node 24+ and npm.
func Prepare(ctx context.Context, cache string, pi Source, out string, stdout io.Writer) (err error) {
	pins, err := sourcePins()
	if err != nil {
		return err
	}
	for _, tool := range []string{"git", "node", "npm"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("preparing Pi requires %s in PATH", tool)
		}
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
	// The patch tests belong to the pinned commit; other commits (bench/build-variants.ts takes patches out) may lack them.
	info := preparedInfo{Pitch: Version, Pi: pi, Tested: pi.Commit == pins.Pi.Commit}
	if info.Node, err = r.output("", "node", "--version"); err != nil {
		return err
	}
	var major int
	if _, err = fmt.Sscanf(info.Node, "v%d.", &major); err != nil || major < 24 {
		return fmt.Errorf("preparing Pi requires Node 24 or newer; found %s", info.Node)
	}
	if info.NPM, err = r.output("", "npm", "--version"); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Preparing Pi %s from %s %s\nLog: %s\n", strings.TrimPrefix(pi.Tag, "v"), pi.Repository, pi.Commit, log.Name())
	bun, err := getRuntime(r, cache, pins.Bun)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Get Pi source")
	pristine, err := checkout(r, cache, pi, log)
	if err != nil {
		return err
	}
	source := filepath.Join(stage, "pi")
	if err = r.command("", log, "git", "clone", "--no-local", "--no-hardlinks", pristine, source); err != nil {
		return err
	}
	if info.SourceTree, err = r.output(source, "git", "write-tree"); err != nil {
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
	catalogPath := filepath.Join(cache, "models-"+pins.ModelCatalog+".json")
	if err = download(r.ctx, "https://pi.dev/api/models/revisions/"+pins.ModelCatalog+"?types=chat,image,classifier", catalogPath, strings.TrimPrefix(pins.ModelCatalog, "sha256-")); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Install dependencies and build Pi")
	for _, command := range [][]string{{"npm", "ci", "--ignore-scripts", "--no-audit", "--no-fund"}, {"node", "packages/ai/scripts/hydrate-model-catalog.ts", catalogPath}, {"npm", "run", "build:offline"}} {
		if err = r.command(source, log, command[0], command[1:]...); err != nil {
			return err
		}
	}
	if err = verifySource(r, source, info.SourceTree); err != nil {
		return err
	}
	if info.Tested {
		fmt.Fprintln(stdout, "Test patches")
		for _, test := range piTests {
			name := test.Args[0]
			if name == "bun" {
				name = bun
			}
			if err = r.command(filepath.Join(source, test.Dir), log, name, test.Args[1:]...); err != nil {
				return fmt.Errorf("patch tests: %w", err)
			}
		}
	}
	if info.NPMLock, err = hashFile(filepath.Join(source, "package-lock.json")); err != nil {
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

// unpackPi extracts a prepared Pi into source: the file at path, or the one the release embeds.
func unpackPi(r runner, pins Sources, path, source string, log io.Writer) (info preparedInfo, err error) {
	var archive io.ReadCloser
	if path != "" {
		archive, err = os.Open(path)
	} else if archive, err = runtimeData.Open("runtime-data/pi.tar.gz"); errors.Is(err, fs.ErrNotExist) {
		err = fmt.Errorf("this pitch has no Pi built in (a development build); make one with `pitch prepare OUT.tar.gz` and pass it with --pi")
	}
	if err != nil {
		return info, err
	}
	defer archive.Close()
	if err = os.Mkdir(source, 0700); err != nil {
		return info, err
	}
	cmd := exec.CommandContext(r.ctx, "tar", "-xzf", "-", "-C", source)
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = r.env, archive, log, log
	protectProcess(cmd)
	if err = cmd.Run(); err != nil {
		return info, fmt.Errorf("unpack Pi: %w", err)
	}
	data, err := os.ReadFile(filepath.Join(source, preparedFile))
	if err != nil {
		return info, err
	}
	if err = json.Unmarshal(data, &info); err != nil {
		return info, err
	}
	if info.Pitch != Version {
		return info, fmt.Errorf("Pi was prepared by pitch %s, not %s", info.Pitch, Version)
	}
	if path == "" && info.Pi.Commit != pins.Pi.Commit {
		return info, fmt.Errorf("the built-in Pi is %s, not the pinned %s", info.Pi.Commit, pins.Pi.Commit)
	}
	return info, nil
}
