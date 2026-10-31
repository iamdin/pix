package pitch

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func resolveExtensions(r runner, pi, stage string, values []string, settings string, log io.Writer) ([]Extension, error) {
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
	request := filepath.Join(stage, "extension-request.json")
	output := filepath.Join(stage, "extensions.json")
	if err := writeJSON(request, map[string]any{"pi": pi, "stage": stage, "cwd": cwd, "userHome": home, "values": values, "settings": settings, "output": output}); err != nil {
		return nil, err
	}
	script := filepath.Join(stage, "recipe", "resolve-extensions.mjs")
	if err := r.command(stage, log, "node", script, request); err != nil {
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
