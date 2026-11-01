package pix

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestInstallationLifecycle(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "pix")
	t.Setenv("PIX_HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(root, "agent"))
	writeFile(t, filepath.Join(root, "agent", "settings.json"), `{"packages": ["npm:x"]}`, 0644)
	var settings []string
	buildFunc = func(_ context.Context, opts Options, _ io.Writer) error {
		settings = append(settings, opts.Settings)
		writeFile(t, filepath.Join(opts.Out, "pi"), "#!/bin/sh\n", 0755)
		return writeJSON(filepath.Join(opts.Out, "pix-build.json"), Manifest{Schema: 1, BuiltAt: time.Now().String()})
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
	must(in.compile(context.Background(), false, 0, io.Discard))
	must(in.compile(context.Background(), true, 0, io.Discard))
	must(in.compile(context.Background(), true, 0, io.Discard))
	if want := filepath.Join(root, "agent", "settings.json"); !slices.Equal(settings, []string{"", want, want}) {
		t.Fatalf("settings per build: %q", settings)
	}
	if ids, _ := in.versions(); len(ids) != 1 || in.current() != ids[0] {
		t.Fatalf("versions: %v, current %s", ids, in.current())
	}
	must(status(io.Discard))
	must(uninstall(io.Discard))
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
