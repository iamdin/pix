package pix

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	// pix follows Pi releases itself: no notice of a newer Pi.
	out.WriteString("process.env.PI_SKIP_VERSION_CHECK ??= '1';\n")
	fmt.Fprintf(&out, "await import(%s);\nawait import(%s);\nconst { setupCli } = await import(%s);\nconst { main } = await import(%s);\n", jsString(filepath.Join(src, "bun/sandbox-env-setup.ts")), jsString(filepath.Join(src, "bun/runtime-setup.ts")), jsString(filepath.Join(src, "cli/setup.ts")), jsString(filepath.Join(src, "main.ts")))
	// `pix update` updates pix and the Pi it holds; only a build run directly gets here. Pi's self-update would
	// install the original Pi instead.
	out.WriteString(`let args = process.argv.slice(2);
const rest = args.slice(1);
if (args[0] === 'update' && (rest.some((a) => ['--self', '--all', 'self', 'pi'].includes(a)) || !rest.some((a) => ['--extensions', '--extension', '--models'].includes(a) || !a.startsWith('-')))) {
 console.error('pix: run pix update');
 process.exit(2);
}
`)
	// Pi reads a subcommand only as the first argument.
	out.WriteString("const command = ['install', 'remove', 'uninstall', 'update', 'list', 'config', 'auth', 'mcp'].includes(args[0] ?? '');\n")
	if len(extensions) > 0 {
		// Compiled extensions load by their entry (-e); Pi then skips the same packages' extensions on disk.
		var sources []string
		for _, e := range extensions {
			if e.Source != "" && !slices.Contains(sources, e.Source) {
				sources = append(sources, e.Source)
			}
		}
		data, _ := json.Marshal(sources)
		fmt.Fprintf(&out, "const compiled = new Set(%s);\nconst { DefaultPackageManager } = await import(%s);\n", data, jsString(filepath.Join(src, "core/package-manager.ts")))
		out.WriteString(`const resolve = DefaultPackageManager.prototype.resolve;
DefaultPackageManager.prototype.resolve = async function (...rest) {
 const paths = await resolve.apply(this, rest);
 if (!command) paths.extensions = paths.extensions.filter((r) => !compiled.has(r.metadata.source));
 return paths;
};
if (!command) args = [`)
		for _, extension := range extensions {
			fmt.Fprintf(&out, "'-e', %s, ", jsString(extension.Entry))
		}
		out.WriteString("...args];\n")
	}
	// In the TUI, the footer says that this is pix, and pix's notices (PIX_NOTICE: another release, a failed compile)
	// are shown as Pi shows a new Pi.
	fmt.Fprintf(&out, "const pixVersion = %s;\n", jsString(Version))
	out.WriteString(`const notices = (process.env.PIX_NOTICE ?? '').split('\n').filter(Boolean);
delete process.env.PIX_NOTICE;
let shown = false;
const pixInfo = { name: 'pix', hidden: true, factory: (pi: any) => {
 pi.on('session_start', (_event: unknown, ctx: any) => {
  if (!ctx.hasUI) return;
  ctx.ui.setStatus('pix', 'pix ' + pixVersion);
  if (shown) return;
  shown = true;
  for (const notice of notices) ctx.ui.notify(notice, 'info');
 });
} };
setupCli();
await main(args, { extensionFactories: [pixInfo] });
`)
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
	// One copy of Node's types: an extension's own (often newer) @types/node conflicts with Pi's.
	paths["@types/node"] = []string{filepath.Join(pi, "node_modules/@types/node")}
	paths["@types/node/*"] = []string{filepath.Join(pi, "node_modules/@types/node/*")}
	return map[string]any{"compilerOptions": map[string]any{"target": "ESNext", "module": "ESNext", "moduleResolution": "Bundler", "strict": true, "resolveJsonModule": true, "skipLibCheck": true, "allowImportingTsExtensions": true, "paths": paths}, "include": []string{entry, filepath.Join(pi, "packages/coding-agent/src/utils/highlight-js.d.ts"), filepath.Join(pi, "packages/coding-agent/src/bun/quickjs-wasm.d.ts")}}, nil
}
