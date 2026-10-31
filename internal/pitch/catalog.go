package pitch

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const Version = "0.1.0-alpha.10"

const defaultFeatures = "all"
const allFeatures = "streaming-markdown,highlight-cache,streaming-tool-arguments,write-call-highlight,native-image-resize"

//go:embed data
var assets embed.FS

type Source struct {
	Repository string `json:"repository"`
	Tag        string `json:"tag,omitempty"`
	Commit     string `json:"commit"`
}
type RuntimePin struct {
	Delivery     string `json:"delivery"`
	Repository   string `json:"repository"`
	Commit       string `json:"commit"`
	WebKitCommit string `json:"webkit_commit"`
	Revision     string `json:"revision"`
	Platform     string `json:"platform"`
	URL          string `json:"url"`
	SHA256       string `json:"sha256"`
	BinarySHA256 string `json:"binary_sha256"`
	Member       string `json:"member"`
}
type Sources struct {
	Schema       int        `json:"schema"`
	Pi           Source     `json:"pi"`
	Bun          RuntimePin `json:"bun"`
	ModelCatalog string     `json:"model_catalog"`
}

// Test is a test command run in a package of the patched source; "bun" runs the pinned Bun.
type Test struct {
	Dir  string   `json:"dir"`
	Args []string `json:"args"`
}

// Feature is one optimization method from docs/pi-perf/<id>/, applied in any order and any subset.
type Feature struct {
	ID     string `json:"id"`
	Patch  string `json:"patch"`
	SHA256 string `json:"sha256"`
	Note   string `json:"note"`
	Tests  []Test `json:"tests"`
}

func vitest(dir string, files ...string) Test {
	return Test{dir, append([]string{"npm", "exec", "--", "vitest", "run"}, files...)}
}

var features = []Feature{
	{"streaming-markdown", "streaming-markdown.patch", "844ffcb65555a980fcd9fc614494d1a70153f3e3a87f15e5e42bf2764a7614cd", "A streamed reply lexes and renders only the blocks that changed.", []Test{
		{"packages/tui", []string{"node", "--test", "test/markdown-incremental-lex.test.ts", "test/markdown-render-memo.test.ts", "test/render-memo.test.ts", "test/markdown.test.ts"}},
		vitest("packages/coding-agent", "test/streaming-markdown-render.test.ts", "test/assistant-message.test.ts"),
	}},
	{"highlight-cache", "highlight-cache.patch", "0016aa79e6b14807ea60c15ab92883eb0bcda2ce2f605baa0edcee56bbb6ab37", "Highlighted code blocks are kept instead of highlighted again each frame.", []Test{
		vitest("packages/coding-agent", "test/highlight-code-cache.test.ts", "test/syntax-highlight.test.ts"),
	}},
	{"streaming-tool-arguments", "streaming-tool-arguments.patch", "8caddc91b9f9cd813ef9b0b6ee7c169c5af888c10f34ecacf060fc83e9350df6", "Streamed tool arguments are re-parsed geometrically less often; raw deltas and final arguments stay complete.", []Test{
		vitest("packages/ai", "test/streaming-json-budget.test.ts", "test/streaming-json-providers.test.ts"),
	}},
	{"write-call-highlight", "write-call-highlight.patch", "778719a4313192a6f55786908a6e8ac81d512601c96551d1b899daed3355065a", "A collapsed write call highlights only the lines it shows.", []Test{
		vitest("packages/coding-agent", "test/write-call-collapsed-highlight.test.ts"),
	}},
	{"native-image-resize", "native-image-resize.patch", "052cfbba781fc133fdb0bbdd4c6cc6412eecb171edd35905e4cf6500e466536b", "Large images are resized with Bun.Image instead of Photon, whose WebAssembly runs without a JIT in AOT builds.", []Test{
		{"packages/coding-agent", []string{"bun", "test", "./test/image-resize-bun.bun.ts"}},
		vitest("packages/coding-agent", "test/image-processing.test.ts", "test/image-resize-callers.test.ts"),
	}},
}

func selectFeatures(value string) ([]Feature, error) {
	if value == "all" {
		value = allFeatures
	}
	if value == "none" {
		return []Feature{}, nil
	}
	requested := map[string]bool{}
	for _, id := range strings.Split(value, ",") {
		if requested[id] {
			return nil, fmt.Errorf("duplicate feature: %s", id)
		}
		requested[id] = true
	}
	result := []Feature{}
	for _, f := range features {
		if requested[f.ID] {
			result = append(result, f)
			delete(requested, f.ID)
		}
	}
	if len(requested) != 0 {
		return nil, fmt.Errorf("unknown patch selection: %s", value)
	}
	return result, nil
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
