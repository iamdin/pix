package pitch

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func put(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestPins(t *testing.T) {
	pins, err := sourcePins()
	if err != nil {
		t.Fatal(err)
	}
	if pins.Pi.Tag != "v1.4.0" || pins.Pi.Branch != "pitch-"+pins.Pi.Tag || pins.Pi.Repository != "https://github.com/getpie-dev/pi.git" || len(pins.Pi.Commit) != 40 || pins.Bun.Repository != "https://github.com/oven-sh/bun.git" || pins.Bun.Platform != "darwin/arm64" || len(pins.Bun.Commit) != 40 || len(pins.Bun.WebKitCommit) != 40 {
		t.Fatalf("unexpected pins: %+v", pins)
	}
}
func TestCLI(t *testing.T) {
	for _, c := range []struct {
		args []string
		code int
	}{
		{nil, 0}, {[]string{"--version"}, 0}, {[]string{"doctor"}, 1}, {[]string{"build", "--help"}, 0},
		{[]string{"build", "--bun-version", "latest"}, 1}, {[]string{"build", "--pi-version", "main"}, 1}, {[]string{"build", "extra"}, 1}, {[]string{"verify"}, 1}, {[]string{"wat"}, 1},
	} {
		var out, errors bytes.Buffer
		if got := Main(context.Background(), c.args, &out, &errors); got != c.code {
			t.Fatalf("%v: %d: %s", c.args, got, errors.String())
		}
	}
}
func TestExtensionSelectionAndAutomaticEntry(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "with space'quote.ts")
	put(t, entry, "throw new Error('must not execute during selection')")
	selected, err := checkExtensionEntry(entry)
	if err != nil {
		t.Fatal(err)
	}
	source := entrySource(dir, []Extension{selected})
	for _, want := range []string{"? args : ['--no-extensions', ", "'-e', 'builtin:codemode', ", "'-e', 'builtin:mcp', '-e', " + jsString(selected.Entry), "...args]", "PI_PACKAGE_DIR", "process.exit(2)"} {
		if !strings.Contains(source, want) {
			t.Fatalf("missing %s", want)
		}
	}
	if strings.Contains(source, "extensionFactories") || strings.Contains(source, "import("+jsString(selected.Entry)) {
		t.Fatal("plugin should be an extra compiler entry, not a generated factory")
	}
	for _, bad := range []string{dir, entry + "missing"} {
		if _, err := checkExtensionEntry(bad); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
}
func TestSharedSDKConfig(t *testing.T) {
	dir := t.TempDir()
	put(t, filepath.Join(dir, "packages/ai/package.json"), `{"name":"@earendil-works/pi-ai","main":"dist/index.js","exports":{".":{"types":"./dist/index.d.ts","import":"dist/index.js"},"./oauth":{"import":"./dist/oauth.js"}}}`)
	config, err := compilerConfig(dir, "entry.ts")
	if err != nil {
		t.Fatal(err)
	}
	paths := config["compilerOptions"].(map[string]any)["paths"].(map[string][]string)
	if paths["@earendil-works/pi-ai"][0] != filepath.Join(dir, "packages/ai/src/index.ts") || paths["@mariozechner/pi-ai/oauth"][0] != filepath.Join(dir, "packages/ai/src/oauth.ts") {
		t.Fatalf("wrong sharing: %v", paths)
	}
}
func TestDownloadIntegrityAndCancellation(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; io.WriteString(w, "official payload") }))
	defer server.Close()
	dir := t.TempDir()
	target := filepath.Join(dir, "file")
	hash := hashBytes([]byte("official payload"))
	if err := download(context.Background(), server.URL, target, hash); err != nil {
		t.Fatal(err)
	}
	if err := download(context.Background(), server.URL, target, hash); err != nil || requests != 1 {
		t.Fatalf("cache: %v %d", err, requests)
	}
	put(t, target, "user-modified")
	if err := download(context.Background(), server.URL, target, hash); err == nil {
		t.Fatal("accepted modified cache")
	}
	data, _ := os.ReadFile(target)
	if string(data) != "user-modified" {
		t.Fatal("overwrote cached file")
	}
	bad := filepath.Join(dir, "bad")
	if err := download(context.Background(), server.URL, bad, strings.Repeat("0", 64)); err == nil || exists(bad) {
		t.Fatal("published corrupt download")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := download(ctx, server.URL, filepath.Join(dir, "cancelled"), hash); err == nil {
		t.Fatal("ignored cancellation")
	}
}
func TestRuntimeArchive(t *testing.T) {
	for _, kind := range []string{"good", "duplicate", "symlink", "missing"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			archive := filepath.Join(dir, "archive.zip")
			f, err := os.Create(archive)
			if err != nil {
				t.Fatal(err)
			}
			z := zip.NewWriter(f)
			header := &zip.FileHeader{Name: "release/bun", Method: zip.Deflate}
			header.SetMode(0755)
			if kind == "symlink" {
				header.SetMode(os.ModeSymlink | 0755)
			}
			if kind != "missing" {
				w, _ := z.CreateHeader(header)
				io.WriteString(w, "runtime")
			}
			if kind == "duplicate" {
				w, _ := z.Create("release/bun")
				io.WriteString(w, "other")
			}
			w, _ := z.Create("../outside")
			io.WriteString(w, "never extract")
			z.Close()
			f.Close()
			target := filepath.Join(dir, "bun")
			err = extractRuntime(archive, target, "release/bun")
			if (err == nil) != (kind == "good") {
				t.Fatalf("%s: %v", kind, err)
			}
			if exists(filepath.Join(filepath.Dir(dir), "outside")) {
				t.Fatal("archive traversal")
			}
			if kind == "good" {
				if err := extractRuntime(archive, target, "release/bun"); err == nil {
					t.Fatal("overwrote runtime")
				}
			}
		})
	}
}
func TestManifestVerification(t *testing.T) {
	for _, kind := range []string{"good", "tampered", "extra", "missing", "symlink", "incomplete", "traversal", "not-executable"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			put(t, filepath.Join(dir, "pi"), "binary")
			os.Chmod(filepath.Join(dir, "pi"), 0700)
			put(t, filepath.Join(dir, "package.json"), "{}")
			put(t, filepath.Join(dir, "recipe/sources.lock.json"), "{}")
			hashes, err := fileHashes(dir)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "tampered":
				put(t, filepath.Join(dir, "pi"), "changed")
			case "extra":
				put(t, filepath.Join(dir, "extra"), "extra")
			case "missing":
				os.Remove(filepath.Join(dir, "pi"))
			case "symlink":
				os.Symlink("pi", filepath.Join(dir, "link"))
			case "incomplete":
				put(t, filepath.Join(dir, ".pitch-incomplete"), "incomplete")
			case "traversal":
				hashes["../escape"] = hashes["pi"]
				delete(hashes, "pi")
			case "not-executable":
				os.Chmod(filepath.Join(dir, "pi"), 0600)
			}
			if err := writeJSON(filepath.Join(dir, "pitch-build.json"), Manifest{Schema: 1, Files: hashes}); err != nil {
				t.Fatal(err)
			}
			err = Verify(dir)
			if (err == nil) != (kind == "good") {
				t.Fatalf("%s: %v", kind, err)
			}
		})
	}
}
func TestBuildEnvironment(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "not-for-builds")
	t.Setenv("BUN_AOT", "0")
	t.Setenv("PI_PACKAGE_DIR", "private")
	t.Setenv("NODE_OPTIONS", "--require=private")
	env := strings.Join(buildEnvironment("/isolated/home", "/isolated/cache"), "\n")
	for _, name := range []string{"OPENAI_API_KEY=", "BUN_AOT=", "PI_PACKAGE_DIR=", "NODE_OPTIONS="} {
		if strings.Contains(env, name) {
			t.Fatal("leaked " + name)
		}
	}
}
func TestProcessCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := (runner{ctx: ctx}).output("", "/bin/sh", "-c", "sleep 30")
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("cancellation: %v %s", err, time.Since(start))
	}
}
func TestSourceGuard(t *testing.T) {
	dir := t.TempDir()
	r := runner{context.Background(), append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")}
	git := func(args ...string) string {
		t.Helper()
		out, err := r.output(dir, "git", args...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	git("init", "-q")
	put(t, filepath.Join(dir, "source"), "original")
	git("add", "source")
	git("-c", "user.name=test", "-c", "user.email=test@localhost", "-c", "commit.gpgsign=false", "commit", "-qm", "fixture")
	tree := git("write-tree")
	if err := verifySource(r, dir, tree); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(dir, "source"), "user edit")
	if err := verifySource(r, dir, tree); err == nil {
		t.Fatal("accepted dirty source")
	}
}
func TestManifestJSON(t *testing.T) {
	pins, err := sourcePins()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(pins)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Pi-Bolt") || strings.Contains(string(data), "/canary/") {
		t.Fatal("floating or obsolete toolchain dependency")
	}
}
