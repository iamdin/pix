package pix

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

func resolveExtensions(r runner, js, pi, stage string, values []string, settings string, installed bool, log io.Writer) ([]Extension, error) {
	if len(values) == 0 && settings == "" {
		return []Extension{}, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	agentDir := ""
	if installed {
		if agentDir, err = linkInstalled(filepath.Dir(settings), filepath.Join(stage, "agent"), pi); err != nil {
			return nil, err
		}
	}
	request := filepath.Join(stage, "extension-request.json")
	output := filepath.Join(stage, "extensions.json")
	if err := writeJSON(request, map[string]any{"pi": pi, "stage": stage, "cwd": cwd, "userHome": home, "values": values, "settings": settings, "agentDir": agentDir, "output": output}); err != nil {
		return nil, err
	}
	script := filepath.Join(stage, "recipe", "resolve-extensions.mjs")
	if err := r.command(stage, log, js, script, request); err != nil {
		return nil, fmt.Errorf("resolve extension packages (see build.log): %w", err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		return nil, err
	}
	var resolved struct{ Extensions []Extension }
	if err := json.Unmarshal(data, &resolved); err != nil {
		return nil, err
	}
	for i, extension := range resolved.Extensions {
		checked, err := checkExtensionEntry(extension.Entry)
		if err != nil {
			return nil, err
		}
		resolved.Extensions[i].Entry = checked.Entry
		resolved.Extensions[i].SHA256 = checked.SHA256
	}
	return resolved.Extensions, nil
}

// linkInstalled mirrors the packages Pi installed (agent/npm and agent/git) into dir with hard links, which take no
// space, so that they compile together with Pi: every @types/node in them becomes Pi's, as a second, newer copy breaks
// Pi's types. A copy is made only where links are impossible (another file system).
func linkInstalled(agent, dir, pi string) (string, error) {
	for _, name := range []string{"npm", "git"} {
		root := filepath.Join(agent, name)
		if !exists(root) {
			continue
		}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(agent, path)
			target := filepath.Join(dir, rel)
			switch {
			case d.IsDir() && d.Name() == ".git":
				return filepath.SkipDir
			case d.IsDir() && d.Name() == "node" && filepath.Base(filepath.Dir(path)) == "@types":
				if err := os.Symlink(filepath.Join(pi, "node_modules/@types/node"), target); err != nil {
					return err
				}
				return filepath.SkipDir
			case d.IsDir():
				return os.MkdirAll(target, 0755)
			case d.Type()&fs.ModeSymlink != 0:
				link, err := os.Readlink(path)
				if err != nil {
					return err
				}
				return os.Symlink(link, target)
			}
			if os.Link(path, target) == nil {
				return nil
			}
			return copyFile(path, target, 0644)
		})
		if err != nil {
			return "", err
		}
	}
	return dir, nil
}
