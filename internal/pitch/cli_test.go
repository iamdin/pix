package pitch

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIPatchSelection(t *testing.T) {
	var out, stderr bytes.Buffer
	if code := Main(context.Background(), []string{"features", "--json"}, &out, &stderr); code != 0 {
		t.Fatalf("%d: %s", code, &stderr)
	}
	var catalog struct {
		Default  []string
		Features []Feature
		Pi       Source
	}
	if err := json.Unmarshal(out.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	if catalog.Default == nil || len(catalog.Default) != 4 || len(catalog.Features) != 4 || len(catalog.Pi.Commit) != 40 {
		t.Fatalf("catalog: %s", &out)
	}
	for _, args := range [][]string{
		{"features", "extra"}, {"features", "--unknown"},
		{"build", "--features", "unknown"}, {"build", "--features", ""}, {"build", "--experimental"},
	} {
		out.Reset()
		stderr.Reset()
		if code := Main(context.Background(), args, &out, &stderr); code != 1 {
			t.Fatalf("accepted %v", args)
		}
	}
	for _, selection := range []string{"all", "none", "write-call-highlight"} {
		out.Reset()
		stderr.Reset()
		args := []string{"build", "--features", selection, "--out", t.TempDir()}
		if code := Main(context.Background(), args, &out, &stderr); code != 1 || !strings.Contains(stderr.String(), "output already exists") {
			t.Fatalf("%v: %d: %s", args, code, &stderr)
		}
	}
	out.Reset()
	stderr.Reset()
	if code := Main(context.Background(), []string{"build", "--patch", filepath.Join(t.TempDir(), "missing.patch")}, &out, &stderr); code != 1 || !strings.Contains(stderr.String(), "patch") {
		t.Fatalf("patch input: %d: %s", code, &stderr)
	}
}
