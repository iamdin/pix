package pitch

import (
	"context"
	"os"
	"path/filepath"
)

// runtime-data/<goos>-<goarch> holds bun.zip and, in releases, pi.tar.gz (see Prepare); neither is in git.
// runtime_<goos>_<goarch>.go embeds the one for the platform pitch is built for.

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
	bun, err := runtimeData.ReadFile(runtimeDataDir + "/bun.zip")
	if err != nil {
		f.Close()
		return err
	}
	_, writeErr := f.Write(bun)
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
