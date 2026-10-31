package pitch

import (
	"context"
	_ "embed"
	"os"
	"path/filepath"
)

//go:embed runtime-data/bun.zip
var bundledRuntime []byte

// Canary assets are disposable; keep the exact validated compiler with the CLI.
func cacheRuntimeArchive(ctx context.Context, target, expected string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if exists(target) {
		return requireHash(target, expected)
	}
	f, err := os.CreateTemp(filepath.Dir(target), "runtime-*.part")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(bundledRuntime)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := requireHash(f.Name(), expected); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Link(f.Name(), target); err != nil {
		if !os.IsExist(err) {
			return err
		}
		return requireHash(target, expected)
	}
	return nil
}
