package pix

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Assets on pix's GitHub release, for the platform pix runs on: the Pi that the release compiled, and the prepared Pi
// that `pix compile` compiles with the user's packages. Bun is inside pix. scripts/release.ts sets their SHA-256 sums:
// -ldflags "-X github.com/iamdin/pix/internal/pix.releaseSums=NAME=SUM,NAME=SUM"; development builds have none.
var releaseSums string

// releaseBase is replaced in tests.
var releaseBase = "https://github.com/iamdin/pix/releases/download/v" + Version

func prebuiltAsset() string { return "pi-" + runtime.GOOS + "-" + runtime.GOARCH + ".tar.gz" }
func sourceAsset() string   { return "pi-source-" + runtime.GOOS + "-" + runtime.GOARCH + ".tar.gz" }

func releaseSum(name string) string {
	for _, pair := range strings.Split(releaseSums, ",") {
		if n, sum, ok := strings.Cut(pair, "="); ok && n == name {
			return sum
		}
	}
	return ""
}

// fetchAsset downloads a release asset into dir, checked against its sum, and returns its path.
func fetchAsset(ctx context.Context, dir, name string) (string, error) {
	sum := releaseSum(name)
	if sum == "" {
		return "", fmt.Errorf("this pix is a development build without release assets: use `pix compile --out DIR --pi PI.tar.gz`")
	}
	path := filepath.Join(dir, name)
	if err := download(ctx, releaseBase+"/"+name, path, sum); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("download %s: %w", name, err)
	}
	return path, nil
}
