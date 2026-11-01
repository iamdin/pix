package pix

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinkInstalled(t *testing.T) {
	root := t.TempDir()
	agent, dir, pi := filepath.Join(root, "agent"), filepath.Join(root, "out"), filepath.Join(root, "pi")
	put(t, filepath.Join(agent, "npm/node_modules/a/index.js"), "a")
	put(t, filepath.Join(agent, "npm/node_modules/@types/node/index.d.ts"), "newer")
	put(t, filepath.Join(agent, "git/github.com/x/y/.git/HEAD"), "ref")
	put(t, filepath.Join(agent, "git/github.com/x/y/index.ts"), "y")
	if err := os.Symlink("a", filepath.Join(agent, "npm/node_modules/b")); err != nil {
		t.Fatal(err)
	}
	if _, err := linkInstalled(agent, dir, pi); err != nil {
		t.Fatal(err)
	}
	src, _ := os.Stat(filepath.Join(agent, "npm/node_modules/a/index.js"))
	dst, err := os.Stat(filepath.Join(dir, "npm/node_modules/a/index.js"))
	if err != nil || !os.SameFile(src, dst) {
		t.Fatalf("not a hard link: %v", err)
	}
	if link, _ := os.Readlink(filepath.Join(dir, "npm/node_modules/@types/node")); link != filepath.Join(pi, "node_modules/@types/node") {
		t.Fatalf("@types/node -> %q", link)
	}
	if link, _ := os.Readlink(filepath.Join(dir, "npm/node_modules/b")); link != "a" {
		t.Fatalf("symlink -> %q", link)
	}
	if exists(filepath.Join(dir, "git/github.com/x/y/.git")) || !exists(filepath.Join(dir, "git/github.com/x/y/index.ts")) {
		t.Fatal("git checkout mirrored wrongly")
	}
}
