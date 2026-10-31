package pitch

import (
	"context"
	"embed"
	"io"
	"os"
	"path/filepath"
)

// runtime-data holds bun.zip and, in releases, pi.tar.gz (see Prepare); neither is in git.
//
//go:embed runtime-data
var runtimeData embed.FS

// writeRuntimeData copies an embedded runtime-data file to w.
func writeRuntimeData(name string, w io.Writer) error {
	f, err := runtimeData.Open("runtime-data/" + name)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

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
	writeErr := writeRuntimeData("bun.zip", f)
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
