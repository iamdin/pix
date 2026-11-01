package pix

import (
	"context"
	"io"
	"os"
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
	if ids, _ := in.versions(); len(ids) != 2 || in.current() != ids[1] {
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
