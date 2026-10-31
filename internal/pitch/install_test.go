package pitch

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstallLifecycle(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "pitch")
	t.Setenv("PITCH_HOME", home)
	t.Setenv("HOME", root)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(root, "agent"))
	stockDir := filepath.Join(root, "stock")
	writeFile(t, filepath.Join(stockDir, "bin", "pi"), "#!/bin/sh\necho STOCK \"$@\"\n", 0755)
	stockPackage := filepath.Join(stockDir, "package.json")
	writeFile(t, stockPackage, "{\n\t\"name\": \"@earendil-works/pi-coding-agent\",\n\t\"version\": \"1.4.0\"\n}\n", 0644)
	t.Setenv("PATH", filepath.Join(stockDir, "bin")+":/usr/bin:/bin")

	buildFunc = func(_ context.Context, opts Options, _ io.Writer) error {
		writeFile(t, filepath.Join(opts.Out, "pi"), "#!/bin/sh\necho PITCH \"$@\"\n", 0755)
		writeFile(t, filepath.Join(opts.Out, "package.json"), "{\n\t\"version\": \"1.4.0\"\n}\n", 0644)
		return writeJSON(filepath.Join(opts.Out, "pitch-build.json"), Manifest{Schema: 1, BuiltAt: time.Now().String()})
	}
	defer func() { buildFunc = Build }()

	pi := func(args ...string) string {
		out, err := exec.Command(filepath.Join(home, "bin", "pi"), args...).CombinedOutput()
		if err != nil {
			t.Fatalf("pi %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run := func(f func() error) {
		if err := f(); err != nil {
			t.Fatal(err)
		}
	}

	run(func() error { return install(context.Background(), "", io.Discard) })
	if got := pi("hi"); got != "PITCH hi" {
		t.Fatalf("installed: %q", got)
	}
	run(func() error { return setOff(true, io.Discard) })
	if got := pi("hi"); got != "STOCK hi" {
		t.Fatalf("off: %q", got)
	}
	run(func() error { return setOff(false, io.Discard) })
	if got := pi("install", "npm:x"); !strings.HasPrefix(got, "STOCK install npm:x") || !strings.Contains(got, "pitch update") {
		t.Fatalf("package commands go to the original Pi: %q", got)
	}
	writeFile(t, stockPackage, "{\n\t\"version\": \"1.10.0\"\n}\n", 0644)
	if got := pi("hi"); !strings.HasSuffix(got, "STOCK hi") || !strings.Contains(got, "1.10.0") {
		t.Fatalf("a newer original Pi runs instead: %q", got)
	}
	writeFile(t, stockPackage, "{\n\t\"version\": \"1.3.9\"\n}\n", 0644)
	if got := pi("hi"); got != "PITCH hi" {
		t.Fatalf("an older original Pi does not: %q", got)
	}

	in, err := loadInstallation()
	if err != nil {
		t.Fatal(err)
	}
	first := in.current()
	run(func() error { return update(context.Background(), io.Discard) })
	run(func() error { return update(context.Background(), io.Discard) })
	ids, _ := in.versions()
	if len(ids) != 2 || in.current() != ids[1] {
		t.Fatalf("versions after updates: %v, current %s", ids, in.current())
	}
	run(func() error { return rollback(io.Discard) })
	if in.current() != ids[0] || in.current() == first {
		t.Fatalf("rollback: %s", in.current())
	}
	run(func() error { return status(io.Discard) })
	run(func() error { return uninstall(io.Discard) })
	if exists(home) {
		t.Fatal("uninstall left", home)
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
