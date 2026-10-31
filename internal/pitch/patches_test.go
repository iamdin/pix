package pitch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInternalPatchOrder(t *testing.T) {
	chosen, err := selectFeatures("write-call-highlight,streaming-tool-arguments,highlight-cache")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, feature := range chosen {
		ids = append(ids, feature.ID)
	}
	if strings.Join(ids, ",") != "highlight-cache,streaming-tool-arguments,write-call-highlight" {
		t.Fatalf("unexpected patch order: %v", ids)
	}
}

// Applying a stack proves source compatibility only; its behavior needs separate tests.
func TestInternalPatchStacksApply(t *testing.T) {
	pi := os.Getenv("PITCH_TEST_PI")
	if pi == "" {
		t.Skip("set PITCH_TEST_PI to a local pinned Pi checkout")
	}
	pins, err := sourcePins()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	r := runner{context.Background(), buildEnvironment(filepath.Join(dir, "home"), filepath.Join(dir, "cache"))}
	git := func(t *testing.T, args ...string) string {
		t.Helper()
		out, err := r.output(dir, "git", args...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	git(t, "init", "--bare", "-q")
	git(t, "fetch", "--depth=1", pi, pins.Pi.Commit)
	if git(t, "rev-parse", "FETCH_HEAD") != pins.Pi.Commit {
		t.Fatal("source commit mismatch")
	}
	recipe := filepath.Join(dir, "recipe")
	if err := materializeAssets(recipe); err != nil {
		t.Fatal(err)
	}
	// Every subset, in catalog order; bench/check-patch-orders.ts checks every order.
	stacks := []string{}
	for mask := 0; mask < 1<<len(features); mask++ {
		ids := []string{}
		for i, f := range features {
			if mask&(1<<i) != 0 {
				ids = append(ids, f.ID)
			}
		}
		if len(ids) == 0 {
			ids = []string{"none"}
		}
		stacks = append(stacks, strings.Join(ids, ","))
	}
	for _, stack := range stacks {
		t.Run(stack, func(t *testing.T) {
			git(t, "read-tree", pins.Pi.Commit)
			chosen, err := selectFeatures(stack)
			if err != nil {
				t.Fatal(err)
			}
			for _, feature := range chosen {
				patch := filepath.Join(recipe, feature.Patch)
				if err := requireHash(patch, feature.SHA256); err != nil {
					t.Fatal(err)
				}
				git(t, "apply", "--cached", patch)
			}
			t.Logf("source tree: %s", git(t, "write-tree"))
			if len(chosen) > 0 {
				if _, err := r.output(dir, "git", "apply", "--cached", filepath.Join(recipe, chosen[0].Patch)); err == nil {
					t.Fatal("accepted a patch on an already patched source")
				}
			}
		})
	}
}
