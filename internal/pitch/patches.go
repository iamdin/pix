package pitch

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Patch records the exact replay order and the immutable recipe copy.
type Patch struct {
	Kind   string `json:"kind"`
	Source string `json:"source"`
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}

type localPatch struct {
	source string
	data   []byte
}

func readLocalPatches(paths []string) ([]localPatch, error) {
	result := []localPatch{}
	for _, path := range paths {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		data, err := readPatch(absolute)
		if err != nil {
			return nil, fmt.Errorf("patch %q: %w", path, err)
		}
		result = append(result, localPatch{absolute, data})
	}
	return result, nil
}

func readPatch(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("must be a regular file (not a symlink)")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	current, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, current) || !current.Mode().IsRegular() {
		return nil, fmt.Errorf("file changed while opening")
	}
	const limit = 16 << 20
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > limit {
		return nil, fmt.Errorf("must contain 1 byte to 16 MiB")
	}
	return data, nil
}

func preparePatches(recipe string, chosen []Feature, local []localPatch) ([]Patch, error) {
	result := []Patch{}
	for _, feature := range chosen {
		if err := requireHash(filepath.Join(recipe, feature.Patch), feature.SHA256); err != nil {
			return nil, err
		}
		result = append(result, Patch{"builtin", feature.ID, "recipe/" + feature.Patch, feature.SHA256})
	}
	for i, input := range local {
		name := fmt.Sprintf("custom-%04d.patch", i+1)
		f, err := os.OpenFile(filepath.Join(recipe, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, err
		}
		_, writeErr := f.Write(input.data)
		closeErr := f.Close()
		if writeErr != nil {
			return nil, writeErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		result = append(result, Patch{"local", input.source, "recipe/" + name, hashBytes(input.data)})
	}
	return result, nil
}

func applyPatches(r runner, source, stage string, patches []Patch, log io.Writer) error {
	for i, patch := range patches {
		path := filepath.Join(stage, patch.File)
		if err := requireHash(path, patch.SHA256); err != nil {
			return err
		}
		if err := r.command(source, log, "git", "apply", "--index", "--", path); err != nil {
			return fmt.Errorf("patch %d (%s, %s): %w", i+1, patch.Kind, patch.Source, err)
		}
	}
	return nil
}
