package pitch

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testDiff(before, after string) string {
	return "diff --git a/example.txt b/example.txt\n--- a/example.txt\n+++ b/example.txt\n@@ -1 +1 @@\n-" + before + "\n+" + after + "\n"
}

func TestLocalPatchInputs(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.patch")
	put(t, good, testDiff("a", "b"))
	link := filepath.Join(dir, "link.patch")
	if err := os.Symlink(good, link); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.patch")
	put(t, empty, "")
	large := filepath.Join(dir, "large.patch")
	put(t, large, "")
	if err := os.Truncate(large, (16<<20)+1); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, link, empty, large, good + "missing"} {
		if _, err := readLocalPatches([]string{path}); err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
	inputs, err := readLocalPatches([]string{good})
	if err != nil {
		t.Fatal(err)
	}
	put(t, good, "changed after snapshot")
	if string(inputs[0].data) != testDiff("a", "b") || inputs[0].source != good {
		t.Fatal("input was not snapshotted")
	}
}

func TestLocalPatchReplay(t *testing.T) {
	for _, kind := range []string{"ordered", "reverse", "duplicate", "corrupt", "traversal", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			stage := t.TempDir()
			source := filepath.Join(stage, "pi")
			recipe := filepath.Join(stage, "recipe")
			put(t, filepath.Join(source, "example.txt"), "a\n")
			r := runner{context.Background(), buildEnvironment(filepath.Join(stage, "home"), filepath.Join(stage, "cache"))}
			for _, args := range [][]string{{"init", "-q"}, {"add", "example.txt"}} {
				if err := r.command(source, io.Discard, "git", args...); err != nil {
					t.Fatal(err)
				}
			}
			if err := materializeAssets(recipe); err != nil {
				t.Fatal(err)
			}
			a, b := filepath.Join(stage, "a.patch"), filepath.Join(stage, "b.patch")
			put(t, a, testDiff("a", "b"))
			put(t, b, testDiff("b", "c"))
			paths := []string{a, b}
			switch kind {
			case "reverse":
				paths = []string{b, a}
			case "duplicate":
				paths = []string{a, a}
			case "traversal":
				put(t, a, strings.ReplaceAll(testDiff("a", "b"), "example.txt", "../outside.txt"))
			case "symlink":
				outside := filepath.Join(stage, "outside")
				if err := os.Mkdir(outside, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(source, "link")); err != nil {
					t.Fatal(err)
				}
				if err := r.command(source, io.Discard, "git", "add", "link"); err != nil {
					t.Fatal(err)
				}
				put(t, a, "diff --git a/link/escape b/link/escape\nnew file mode 100644\n--- /dev/null\n+++ b/link/escape\n@@ -0,0 +1 @@\n+escape\n")
			}
			inputs, err := readLocalPatches(paths)
			if err != nil {
				t.Fatal(err)
			}
			patches, err := preparePatches(recipe, inputs)
			if err != nil {
				t.Fatal(err)
			}
			if len(patches) != 2 || patches[0].Source != paths[0] || patches[0].File == patches[1].File {
				t.Fatal("lost order or identity")
			}
			if kind == "corrupt" {
				put(t, filepath.Join(stage, patches[0].File), "corrupt")
			}
			err = applyPatches(r, source, stage, patches, io.Discard)
			if (err == nil) != (kind == "ordered") {
				t.Fatalf("%s: %v", kind, err)
			}
			if kind == "ordered" {
				data, err := os.ReadFile(filepath.Join(source, "example.txt"))
				if err != nil || string(data) != "c\n" {
					t.Fatalf("%q %v", data, err)
				}
			}
			if exists(filepath.Join(stage, "outside", "escape")) || exists(filepath.Join(stage, "outside.txt")) {
				t.Fatal("patch escaped checkout")
			}
		})
	}
}

func TestPatchProvenanceVerification(t *testing.T) {
	for _, kind := range []string{"good", "bad-hash", "duplicate", "unsafe", "unknown-kind"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			put(t, filepath.Join(dir, "pi"), "binary")
			if err := os.Chmod(filepath.Join(dir, "pi"), 0700); err != nil {
				t.Fatal(err)
			}
			put(t, filepath.Join(dir, "package.json"), "{}")
			put(t, filepath.Join(dir, "recipe/sources.lock.json"), "{}")
			patch := Patch{"local", "/input/fix.patch", "recipe/custom-0001.patch", hashBytes([]byte("diff"))}
			put(t, filepath.Join(dir, patch.File), "diff")
			hashes, err := fileHashes(dir)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "bad-hash":
				patch.SHA256 = strings.Repeat("0", 64)
			case "unsafe":
				patch.File = "../escape.patch"
			case "unknown-kind":
				patch.Kind = "remote"
			}
			manifest := Manifest{Schema: 1, Files: hashes, Patches: []Patch{patch}}
			if kind == "duplicate" {
				manifest.Patches = append(manifest.Patches, patch)
			}
			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			put(t, filepath.Join(dir, "pitch-build.json"), string(data))
			if err := Verify(dir); (err == nil) != (kind == "good") {
				t.Fatalf("%s: %v", kind, err)
			}
		})
	}
}
