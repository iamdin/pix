package pix

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestJSONDoesNotOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	put(t, path, "user data")
	if err := writeJSON(path, map[string]int{"new": 1}); err == nil {
		t.Fatal("overwrote JSON")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "user data" {
		t.Fatal("changed original")
	}
}
func TestBuildDoesNotOverwrite(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "output")
	put(t, filepath.Join(out, "keep"), "original")
	err := Build(context.Background(), Options{Out: out, Cache: filepath.Join(root, "cache")}, io.Discard)
	if err == nil {
		t.Fatal("accepted existing output")
	}
	data, _ := os.ReadFile(filepath.Join(out, "keep"))
	if string(data) != "original" {
		t.Fatal("changed original")
	}
	if exists(filepath.Join(root, "cache")) {
		t.Fatal("did work before rejecting existing output")
	}
}
