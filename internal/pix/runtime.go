package pix

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// runtime-data/<goos>-<goarch> holds pi.tar.gz in releases (see Prepare); it is not in git.
// runtime_<goos>_<goarch>.go embeds the one for the platform pix is built for.

// cacheRuntimeArchive downloads the pinned Bun archive from url, once: canary builds disappear upstream, so pix's
// own release keeps them, and the hash is checked either way.
func cacheRuntimeArchive(ctx context.Context, url, target, expected string) error {
	if exists(target) {
		return requireHash(target, expected)
	}
	f, err := os.CreateTemp(filepath.Dir(target), "runtime-*.part")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		f.Close()
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.Close()
		return fmt.Errorf("download Bun: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		f.Close()
		return fmt.Errorf("download Bun: %s: %s", url, resp.Status)
	}
	_, copyErr := io.Copy(f, io.LimitReader(resp.Body, 512<<20))
	closeErr := f.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return fmt.Errorf("download Bun: %w", err)
	}
	if err := requireHash(f.Name(), expected); err != nil {
		return err
	}
	if err := os.Link(f.Name(), target); err != nil && !os.IsExist(err) {
		return err
	}
	return requireHash(target, expected)
}
