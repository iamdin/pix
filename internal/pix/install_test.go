package pix

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeRelease serves a prebuilt Pi for this pix as its release asset.
func fakeRelease(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pi"), "#!/bin/sh\n", 0755)
	writeFile(t, filepath.Join(dir, "package.json"), "{}", 0644)
	writeFile(t, filepath.Join(dir, "recipe", "sources.lock.json"), "{}", 0644)
	files, err := fileHashes(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(dir, "pix-build.json"), Manifest{Schema: 1, Pix: Version, Files: files}); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), prebuiltAsset())
	if out, err := exec.Command("tar", "-czf", archive, "-C", dir, ".").CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	sum, err := hashFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.FileServer(http.Dir(filepath.Dir(archive))))
	t.Cleanup(server.Close)
	base, sums := releaseBase, releaseSums
	releaseBase, releaseSums = server.URL, prebuiltAsset()+"="+sum
	t.Cleanup(func() { releaseBase, releaseSums = base, sums })
	delay := retryDelay
	retryDelay = 0
	t.Cleanup(func() { retryDelay = delay })
}

func TestInstallationLifecycle(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "pix")
	t.Setenv("HOME", root)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("PIX_HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(root, "agent"))
	fakeRelease(t)
	builds := 0
	buildFunc = func(_ context.Context, opts Options, _ io.Writer) error {
		builds++
		if !opts.Installed || opts.Settings != filepath.Join(root, "agent", "settings.json") {
			t.Fatalf("compiles the packages Pi installed: %+v", opts)
		}
		writeFile(t, filepath.Join(opts.Out, "pi"), "#!/bin/sh\n", 0755)
		return writeJSON(filepath.Join(opts.Out, "pix-build.json"), Manifest{Schema: 1, Pix: Version, Extensions: []Extension{{Name: "x"}}})
	}
	defer func() { buildFunc = Build }()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	in, err := loadInstallation()
	must(err)
	must(status(io.Discard))
	must(in.install(context.Background(), io.Discard))
	must(in.compile(context.Background(), 0, io.Discard)) // no packages: the release's Pi again
	if builds != 0 {
		t.Fatal("compiled without packages")
	}
	writeFile(t, filepath.Join(root, "agent", "settings.json"), `{"packages": ["npm:x"]}`, 0644)
	must(in.compile(context.Background(), 0, io.Discard))
	if m, _ := readManifest(filepath.Join(home, "current")); builds != 1 || len(m.Extensions) != 1 {
		t.Fatalf("builds %d, current %+v", builds, m)
	}
	if ids, _ := in.versions(); len(ids) != 1 || in.current() != ids[0] {
		t.Fatalf("versions: %v, current %s", ids, in.current())
	}
	cache, _ := os.UserCacheDir()
	if downloads, _ := filepath.Glob(filepath.Join(cache, "pix", "pi-*")); len(downloads) != 0 {
		t.Fatalf("left downloads: %v", downloads)
	}
	must(status(io.Discard))
	must(uninstall(io.Discard))
	if exists(home) {
		t.Fatal("uninstall left", home)
	}
}

func TestInstallRejectsTamperedAsset(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	fakeRelease(t)
	releaseSums = prebuiltAsset() + "=" + strings.Repeat("0", 64)
	in := &installation{Home: t.TempDir()}
	if err := in.install(context.Background(), io.Discard); err == nil || in.current() != "" {
		t.Fatalf("installed an asset with the wrong sum: %v", err)
	}
	releaseSums = ""
	if err := in.install(context.Background(), io.Discard); err == nil {
		t.Fatal("a development build has no release assets")
	}
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func TestPruneKeepsRunningVersion(t *testing.T) {
	in := &installation{Home: t.TempDir()}
	for _, id := range []string{"a", "b", "c"} {
		writeFile(t, filepath.Join(in.Home, "versions", id, "pi"), "#!/bin/sh\nsleep \"$1\"\n", 0755)
	}
	if err := in.switchTo("c"); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(in.Home, "versions", "a", "pi"), "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	if err := in.prune(nil); err != nil {
		t.Fatal(err)
	}
	if ids, _ := in.versions(); !slices.Equal(ids, []string{"a", "c"}) {
		t.Fatalf("kept %v, want the running a and the current c", ids)
	}
}

func TestUpdateTarget(t *testing.T) {
	for _, c := range []struct {
		args []string
		self bool
		rest []string
	}{
		{[]string{"update"}, true, nil},
		{[]string{"update", "--self"}, true, nil},
		{[]string{"update", "pi", "--force"}, true, nil},
		{[]string{"update", "--all"}, true, []string{"update", "--extensions"}},
		{[]string{"update", "--extensions"}, false, []string{"update", "--extensions"}},
		{[]string{"update", "npm:x"}, false, []string{"update", "npm:x"}},
		{[]string{"update", "--models"}, false, []string{"update", "--models"}},
	} {
		if self, rest := updateTarget(c.args); self != c.self || !slices.Equal(rest, c.rest) {
			t.Errorf("%v: %v %q, want %v %q", c.args, self, rest, c.self, c.rest)
		}
	}
}

// After an update, sync compiles the packages into the new Pi; when that fails it uses the new release's Pi.
func TestSyncAfterUpdate(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(root, "agent"))
	writeFile(t, filepath.Join(root, "agent", "settings.json"), `{"packages": ["npm:x"]}`, 0644)
	fakeRelease(t)
	in := &installation{Home: filepath.Join(root, "pix")}
	old := func(extensions []Extension) {
		dir := filepath.Join(in.Home, "versions", "old")
		writeFile(t, filepath.Join(dir, "pi"), "#!/bin/sh\n", 0755)
		if err := writeJSON(filepath.Join(dir, "pix-build.json"), Manifest{Schema: 1, Pix: "0.0.0", Extensions: extensions}); err != nil {
			t.Fatal(err)
		}
		if err := in.switchTo("old"); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { buildFunc = Build }()
	for _, fails := range []bool{false, true} {
		old([]Extension{{Name: "x"}})
		buildFunc = func(_ context.Context, opts Options, _ io.Writer) error {
			if fails {
				return io.ErrUnexpectedEOF
			}
			writeFile(t, filepath.Join(opts.Out, "pi"), "#!/bin/sh\n", 0755)
			return writeJSON(filepath.Join(opts.Out, "pix-build.json"), Manifest{Schema: 1, Pix: Version, Extensions: []Extension{{Name: "x"}}})
		}
		if err := in.sync(context.Background(), io.Discard, true); err != nil {
			t.Fatal(err)
		}
		m, _ := readManifest(filepath.Join(in.Home, "current"))
		if ids, _ := in.versions(); m.Pix != Version || (len(m.Extensions) > 0) == fails || len(ids) != 1 {
			t.Fatalf("compile fails %v: current %+v, versions %v", fails, m, ids)
		}
	}
}

func TestLockSerializes(t *testing.T) {
	in := &installation{Home: t.TempDir()}
	unlock, err := in.lock(io.Discard, true)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan struct{})
	go func() {
		second, err := in.lock(io.Discard, true)
		if err == nil {
			second()
		}
		close(got)
	}()
	if _, err := in.lock(io.Discard, false); err != errBusy {
		t.Fatalf("without waiting: %v, want errBusy", err)
	}
	select {
	case <-got:
		t.Fatal("two holders of the lock")
	case <-time.After(200 * time.Millisecond):
	}
	unlock()
	<-got
}

func TestCheckLatest(t *testing.T) {
	server := httptest.NewServer(http.RedirectHandler("/iamdin/pix/releases/tag/v9.9.9", http.StatusFound))
	defer server.Close()
	defer func(u string) { latestURL = u }(latestURL)
	latestURL = server.URL
	in := &installation{Home: t.TempDir()}
	if err := in.checkLatest(context.Background()); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(in.Home, "latest")); string(data) != "9.9.9" {
		t.Fatalf("latest %q", data)
	}
}
