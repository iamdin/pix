package pitch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRejectManifestSymlinkBeforeReading(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink("/dev/zero", filepath.Join(dir, "pitch-build.json")); err != nil {
		t.Fatal(err)
	}
	if err := Verify(dir); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("manifest symlink: %v", err)
	}
}
