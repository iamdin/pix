package pix

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func packageTestRunner(t *testing.T) (runner, string, string) {
	t.Helper()
	pi := os.Getenv("PIX_TEST_PI")
	if pi == "" {
		t.Skip("set PIX_TEST_PI to a built, pinned Pi source tree")
	}
	pi, err := filepath.Abs(pi)
	if err != nil {
		t.Fatal(err)
	}
	pins, err := sourcePins()
	if err != nil {
		t.Fatal(err)
	}
	head, err := exec.Command("git", "-C", pi, "rev-parse", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(head)) != pins.Pi.Commit {
		t.Fatalf("test Pi must match the pin: %s %v", head, err)
	}
	stage := t.TempDir()
	if err := materializeAssets(filepath.Join(stage, "recipe")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(stage, "home"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return runner{ctx, buildEnvironment(filepath.Join(stage, "home"), filepath.Join(stage, "cache"))}, pi, stage
}

func TestPackageLocalSources(t *testing.T) {
	for _, kind := range []string{"file", "tilde", "alias", "manifest", "self", "conventional", "index", "directory", "missing", "empty", "duplicate", "builtin"} {
		t.Run(kind, func(t *testing.T) {
			r, pi, stage := packageTestRunner(t)
			root := t.TempDir()
			t.Chdir(root)
			t.Setenv("HOME", root)
			put(t, filepath.Join(root, ".pi/settings.json"), `{"packages":["npm:must-not-install-this-package"]}`)
			put(t, filepath.Join(root, ".pi/extensions/auto.ts"), "throw new Error('not selected')")
			// Selection must never execute extension code.
			module := "throw new Error('selection executed plugin'); export default function() {}"
			put(t, filepath.Join(root, "single.ts"), module)
			values := []string{"./single.ts"}
			want := 1
			switch kind {
			case "tilde":
				values = []string{"~/single.ts"}
			case "alias":
				values = []string{"fixture=" + filepath.Join(root, "single.ts")}
			case "manifest":
				put(t, filepath.Join(root, "pkg/package.json"), `{"name":"@fixture/tools","version":"1.2.3","pi":{"extensions":["src/*.ts","!src/ignored.ts"]}}`)
				put(t, filepath.Join(root, "pkg/src/one.ts"), module)
				put(t, filepath.Join(root, "pkg/src/two.ts"), module)
				put(t, filepath.Join(root, "pkg/src/ignored.ts"), module)
				values, want = []string{"./pkg"}, 2
			case "self": // @plannotator/pi-extension declares its own directory
				put(t, filepath.Join(root, "pkg/package.json"), `{"name":"self","pi":{"extensions":["./"]}}`)
				put(t, filepath.Join(root, "pkg/index.ts"), module)
				values = []string{"./pkg"}
			case "conventional":
				put(t, filepath.Join(root, "pkg/extensions/one.ts"), module)
				values = []string{"./pkg"}
			case "index":
				put(t, filepath.Join(root, "pkg/index.ts"), module)
				values = []string{"./pkg"}
			case "directory":
				put(t, filepath.Join(root, "pkg/one.ts"), module)
				put(t, filepath.Join(root, "pkg/two.ts"), module)
				values, want = []string{"./pkg"}, 2
			case "missing":
				values, want = []string{"./missing.ts"}, 0
			case "empty":
				values, want = []string{""}, 0
			case "duplicate":
				values, want = []string{"./single.ts", "./single.ts"}, 1
			case "builtin":
				values, want = []string{"builtin:codemode"}, 0
			}
			var log bytes.Buffer
			selected, err := resolveExtensions(r, "node", pi, stage, values, "", &log)
			if want == 0 {
				if err == nil {
					t.Fatalf("accepted %v", values)
				}
				return
			}
			if err != nil || len(selected) != want {
				t.Fatalf("resolved=%+v err=%v\n%s", selected, err, &log)
			}
			for _, extension := range selected {
				if len(extension.SHA256) != 64 || strings.Contains(extension.Entry, "ignored") || strings.Contains(extension.Entry, ".pi/") {
					t.Fatalf("unexpected extension: %+v", extension)
				}
				if kind == "manifest" && (extension.Package != "@fixture/tools" || extension.Version != "1.2.3") {
					t.Fatalf("missing package identity: %+v", extension)
				}
			}
			if kind == "alias" && selected[0].Name != "single.ts" {
				t.Fatal(selected[0].Name)
			}
		})
	}
}

func npmTestArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tarball := tar.NewWriter(gz)
	for name, content := range files {
		if err := tarball.WriteHeader(&tar.Header{Name: "package/" + name, Mode: 0644, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarball.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarball.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestPackageNPMDependencies(t *testing.T) {
	r, pi, stage := packageTestRunner(t)
	marker := filepath.Join(stage, "postinstall-must-not-run")
	manifests := map[string]map[string]any{
		"@fixture/plugin": {"name": "@fixture/plugin", "version": "1.2.3", "pi": map[string]any{"extensions": []string{"extensions"}}, "dependencies": map[string]string{"@fixture/helper": "1.2.3"}, "peerDependencies": map[string]string{"@earendil-works/pi-coding-agent": "*"}, "scripts": map[string]string{"postinstall": "touch " + marker}},
		"@fixture/helper": {"name": "@fixture/helper", "version": "1.2.3", "main": "index.js"},
	}
	archives := map[string][]byte{}
	for name, manifest := range manifests {
		data, _ := json.Marshal(manifest)
		archives[name] = npmTestArchive(t, map[string]string{"package.json": string(data), "extensions/index.ts": "import helper from '@fixture/helper'; export default function() { helper(); }", "index.js": "module.exports = () => 'dependency';"})
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		name := strings.TrimPrefix(req.URL.Path, "/")
		if strings.HasSuffix(name, ".tgz") {
			archive, ok := archives[strings.TrimSuffix(name, ".tgz")]
			if !ok {
				http.NotFound(w, req)
				return
			}
			w.Write(archive)
			return
		}
		manifest, ok := manifests[name]
		if !ok {
			http.NotFound(w, req)
			return
		}
		sum := sha512.Sum512(archives[name])
		version := map[string]any{}
		for key, value := range manifest {
			version[key] = value
		}
		version["dist"] = map[string]string{"tarball": server.URL + "/" + name + ".tgz", "integrity": "sha512-" + base64.StdEncoding.EncodeToString(sum[:])}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"name": name, "dist-tags": map[string]string{"latest": "1.2.3"}, "versions": map[string]any{"1.2.3": version}})
	}))
	defer server.Close()
	r.env = append(r.env, "npm_config_registry="+server.URL)
	var log bytes.Buffer
	selected, err := resolveExtensions(r, "node", pi, stage, []string{"npm:@fixture/plugin@1.2.3"}, "", &log)
	if err != nil || len(selected) != 1 {
		t.Fatalf("%+v %v\n%s", selected, err, &log)
	}
	extension := selected[0]
	if extension.Name != "@fixture/plugin" || extension.Package != "@fixture/plugin" || extension.Version != "1.2.3" || len(extension.DependencyLockSHA256) != 64 {
		t.Fatalf("missing provenance: %+v", extension)
	}
	if exists(marker) {
		t.Fatal("npm lifecycle script ran")
	}
	root := filepath.Dir(filepath.Dir(extension.Entry))
	nodeModules := filepath.Dir(filepath.Dir(root))
	if !exists(filepath.Join(nodeModules, "@fixture/helper/index.js")) {
		t.Fatal("transitive dependency not installed")
	}
	if exists(filepath.Join(nodeModules, "@earendil-works/pi-coding-agent")) {
		t.Fatal("installed duplicate host SDK peer")
	}
	var evidence struct{ Locks []struct{ Content string } }
	data, err := os.ReadFile(filepath.Join(stage, "extensions.json"))
	if err != nil || json.Unmarshal(data, &evidence) != nil || len(evidence.Locks) != 1 {
		t.Fatalf("missing dependency lock: %s %v", data, err)
	}
	if hashBytes([]byte(evidence.Locks[0].Content)) != extension.DependencyLockSHA256 || !strings.Contains(evidence.Locks[0].Content, "@fixture/helper") {
		t.Fatal("dependency lock does not match")
	}
}

func TestPackageGitSources(t *testing.T) {
	for _, source := range []string{"git:example.test/owner/plugin@v1", "https://example.test/owner/plugin@v1", "git:example.test/owner/plugin@branch", "git:example.test/owner/plugin@commit", "equivalent-urls"} {
		t.Run(source, func(t *testing.T) {
			r, pi, stage := packageTestRunner(t)
			repo := filepath.Join(t.TempDir(), "plugin")
			put(t, filepath.Join(repo, "package.json"), `{"name":"git-fixture","version":"1.0.0","pi":{"extensions":["index.ts"]}}`)
			put(t, filepath.Join(repo, "index.ts"), "export default function() {}")
			for _, args := range [][]string{{"init"}, {"add", "."}, {"-c", "user.name=Pix test", "-c", "user.email=pix@example.invalid", "commit", "-m", "fixture"}, {"tag", "v1"}, {"branch", "test-branch"}} {
				if err := r.command(repo, nil, "git", args...); err != nil {
					t.Fatal(err)
				}
			}
			commit, err := r.output(repo, "git", "rev-parse", "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			put(t, filepath.Join(stage, "home/.gitconfig"), fmt.Sprintf("[url %q]\n\tinsteadOf = https://example.test/owner/\n", "file://"+filepath.Dir(repo)+"/"))
			if strings.HasSuffix(source, "@branch") {
				source = strings.TrimSuffix(source, "@branch") + "@test-branch"
			}
			if strings.HasSuffix(source, "@commit") {
				source = strings.TrimSuffix(source, "@commit") + "@" + commit
			}
			values := []string{source}
			if source == "equivalent-urls" {
				values = []string{"git:example.test/owner/plugin@missing-ref", "https://example.test/owner/plugin@v1"}
			}
			var log bytes.Buffer
			selected, err := resolveExtensions(r, "node", pi, stage, values, "", &log)
			if err != nil || len(selected) != 1 {
				t.Fatalf("%+v %v\n%s", selected, err, &log)
			}
			if selected[0].Source != values[len(values)-1] || selected[0].Commit != commit || selected[0].Version != "1.0.0" || selected[0].DependencyLockSHA256 == "" {
				t.Fatalf("missing git provenance: %+v", selected[0])
			}
		})
	}
}
