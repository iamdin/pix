package pitch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Extension struct {
	Name                 string `json:"name"`
	Entry                string `json:"entry"`
	SHA256               string `json:"sha256"`
	Source               string `json:"source,omitempty"`
	Package              string `json:"package,omitempty"`
	Version              string `json:"version,omitempty"`
	Commit               string `json:"commit,omitempty"`
	DependencyLockSHA256 string `json:"dependency_lock_sha256,omitempty"`
}

func checkExtensionEntry(path string) (Extension, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return Extension{}, err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return Extension{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return Extension{}, err
	}
	if !info.Mode().IsRegular() {
		return Extension{}, fmt.Errorf("extension must name a file: %s", path)
	}
	switch filepath.Ext(path) {
	case ".ts", ".js", ".mts", ".mjs", ".cts", ".cjs":
	default:
		return Extension{}, fmt.Errorf("unsupported extension entry: %s", path)
	}
	hash, err := hashFile(path)
	return Extension{Entry: path, SHA256: hash}, err
}
func jsString(value string) string { data, _ := json.Marshal(value); return string(data) }
func entrySource(pi string, extensions []Extension) string {
	src := filepath.Join(pi, "packages/coding-agent/src")
	var out strings.Builder
	out.WriteString("import { dirname } from 'node:path';\nprocess.env.PI_PACKAGE_DIR ??= dirname(process.execPath);\n")
	fmt.Fprintf(&out, "await import(%s);\nawait import(%s);\nconst { setupCli } = await import(%s);\nconst { main } = await import(%s);\n", jsString(filepath.Join(src, "bun/sandbox-env-setup.ts")), jsString(filepath.Join(src, "bun/runtime-setup.ts")), jsString(filepath.Join(src, "cli/setup.ts")), jsString(filepath.Join(src, "main.ts")))
	out.WriteString("const args = process.argv.slice(2);\n")
	out.WriteString("if (['install','remove','uninstall','update','list','config','auth','mcp'].includes(args[0] ?? '')) {\n console.error('This custom build does not manage installations or configuration. Use your installed Pi.');\n process.exit(2);\n}\nsetupCli();\n")
	out.WriteString("await main(['--no-extensions', ")
	for _, extension := range extensions {
		fmt.Fprintf(&out, "'-e', %s, ", jsString(extension.Entry))
	}
	out.WriteString("...args]);\n")
	return out.String()
}
func compilerConfig(pi, entry string) (map[string]any, error) {
	// Share the SDK implementations without the repository-wide ambient JSON declarations.
	targetPath := func(root, target string) string {
		target = strings.TrimPrefix(target, "./")
		if strings.HasPrefix(target, "dist/") {
			target = "src/" + strings.TrimPrefix(target, "dist/")
		}
		if strings.HasSuffix(target, ".js") {
			target = strings.TrimSuffix(target, ".js") + ".ts"
		}
		return filepath.Join(root, target)
	}
	paths := map[string][]string{}
	files, err := filepath.Glob(filepath.Join(pi, "packages/*/package.json"))
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		var p struct {
			Name    string
			Main    string
			Exports map[string]json.RawMessage
		}
		if err := json.Unmarshal(data, &p); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(p.Name, "@earendil-works/") {
			continue
		}
		if p.Main != "" {
			paths[p.Name] = []string{targetPath(filepath.Dir(file), p.Main)}
		}
		for key, value := range p.Exports {
			if key != "." && !strings.HasPrefix(key, "./") {
				continue
			}
			var target string
			if json.Unmarshal(value, &target) != nil {
				var condition struct {
					Import  string
					Default string
				}
				if json.Unmarshal(value, &condition) != nil {
					continue
				}
				target = condition.Import
				if target == "" {
					target = condition.Default
				}
			}
			if target == "" {
				continue
			}
			name := p.Name
			if key != "." {
				name += "/" + strings.TrimPrefix(key, "./")
			}
			paths[name] = []string{targetPath(filepath.Dir(file), target)}
		}
	}
	for name, targets := range paths {
		if strings.HasPrefix(name, "@earendil-works/pi-") {
			paths[strings.Replace(name, "@earendil-works/", "@mariozechner/", 1)] = targets
		}
	}
	paths["typebox"] = []string{filepath.Join(pi, "node_modules/typebox")}
	return map[string]any{"compilerOptions": map[string]any{"target": "ESNext", "module": "ESNext", "moduleResolution": "Bundler", "strict": true, "resolveJsonModule": true, "skipLibCheck": true, "allowImportingTsExtensions": true, "paths": paths}, "include": []string{entry, filepath.Join(pi, "packages/coding-agent/src/utils/highlight-js.d.ts"), filepath.Join(pi, "packages/coding-agent/src/bun/quickjs-wasm.d.ts")}}, nil
}
