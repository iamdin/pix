package pix

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const Version = "0.0.3"

//go:embed data
var assets embed.FS

// Source is Pi: pix's fork (repository, branch) pinned to a commit, which is the upstream release tag plus pix's
// patches, one commit each (docs/pi-perf/<name>/SPEC.md).
type Source struct {
	Repository string `json:"repository"`
	Branch     string `json:"branch,omitempty"`
	Tag        string `json:"tag,omitempty"`
	Commit     string `json:"commit"`
}

// RuntimeAsset is the official Bun release archive for one platform.
type RuntimeAsset struct {
	URL          string `json:"url"`
	SHA256       string `json:"sha256"`
	BinarySHA256 string `json:"binary_sha256"`
	Member       string `json:"member"`
}

// RuntimePin is one Bun build; Platforms is keyed by GOOS/GOARCH. pix embeds the archive for its own platform.
type RuntimePin struct {
	Repository   string                  `json:"repository"`
	Commit       string                  `json:"commit"`
	WebKitCommit string                  `json:"webkit_commit"`
	Revision     string                  `json:"revision"`
	Platforms    map[string]RuntimeAsset `json:"platforms"`
}

// platform is the GOOS/GOARCH pix runs on and builds for.
const platform = runtime.GOOS + "/" + runtime.GOARCH

func (p RuntimePin) host() (RuntimeAsset, error) {
	asset, ok := p.Platforms[platform]
	if !ok {
		return asset, fmt.Errorf("pix does not support %s", platform)
	}
	return asset, nil
}

type Sources struct {
	Schema       int        `json:"schema"`
	Pi           Source     `json:"pi"`
	Bun          RuntimePin `json:"bun"`
	ModelCatalog string     `json:"model_catalog"`
}

// Test is a test command run in a package of the Pi source; "bun" runs the pinned Bun.
type Test struct {
	Dir  string   `json:"dir"`
	Args []string `json:"args"`
}

func vitest(dir string, files ...string) Test {
	return Test{dir, append([]string{"npm", "exec", "--", "vitest", "run"}, files...)}
}

// piTests are the tests of pix's patches, run when Pi is prepared.
var piTests = []Test{
	// streaming-markdown
	{"packages/tui", []string{"node", "--test", "test/markdown-incremental-lex.test.ts", "test/markdown-render-memo.test.ts", "test/render-memo.test.ts", "test/markdown.test.ts"}},
	vitest("packages/coding-agent", "test/streaming-markdown-render.test.ts", "test/assistant-message.test.ts"),
	// highlight-cache
	vitest("packages/coding-agent", "test/highlight-code-cache.test.ts", "test/syntax-highlight.test.ts"),
	// streaming-tool-arguments
	vitest("packages/ai", "test/streaming-json-budget.test.ts", "test/streaming-json-providers.test.ts"),
	// write-call-highlight
	vitest("packages/coding-agent", "test/write-call-collapsed-highlight.test.ts"),
	// native-image-resize
	{"packages/coding-agent", []string{"bun", "test", "./test/image-resize-bun.bun.ts"}},
	vitest("packages/coding-agent", "test/image-processing.test.ts", "test/image-resize-callers.test.ts"),
}

func sourcePins() (Sources, error) {
	var pins Sources
	data, err := assets.ReadFile("data/sources.lock.json")
	if err == nil {
		err = json.Unmarshal(data, &pins)
	}
	return pins, err
}

func materializeAssets(destination string) error {
	return fs.WalkDir(assets, "data", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(destination, strings.TrimPrefix(path, "data"))
		if d.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		data, err := assets.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0600)
	})
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(append(data, '\n'))
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
