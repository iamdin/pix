package pitch

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildInstalledLifecycle(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "pitch")
	t.Setenv("PITCH_HOME", home)
	t.Setenv("HOME", root)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(root, "agent"))
	t.Setenv("PITCH_OFF", "")
	stockDir := filepath.Join(root, "stock")
	stock := filepath.Join(stockDir, "bin", "pi")
	writeFile(t, stock, "#!/bin/sh\necho STOCK \"$@\"\n", 0755)
	stockPackage := filepath.Join(stockDir, "package.json")
	writeFile(t, stockPackage, `{"name": "@earendil-works/pi-coding-agent", "version": "1.4.0"}`, 0644)
	t.Setenv("PATH", filepath.Join(stockDir, "bin")+":/usr/bin:/bin")
	buildFunc = func(_ context.Context, opts Options, _ io.Writer) error {
		writeFile(t, filepath.Join(opts.Out, "pi"), "#!/bin/sh\necho PITCH \"$@\"\n", 0755)
		writeFile(t, filepath.Join(opts.Out, "package.json"), `{"version": "1.4.0"}`, 0644)
		return writeJSON(filepath.Join(opts.Out, "pitch-build.json"), Manifest{Schema: 1, BuiltAt: time.Now().String()})
	}
	defer func() { buildFunc = Build }()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	in := func() *installation {
		in, err := requireInstalled()
		must(err)
		return in
	}
	launches := func(want string, args ...string) {
		t.Helper()
		exe, _, notice, err := in().launch(args)
		must(err)
		if exe != want {
			t.Fatalf("pitch pi %v: %s (%s), want %s", args, exe, notice, want)
		}
	}
	build := filepath.Join(home, "current", "pi")

	must(buildInstalled(context.Background(), "pi", io.Discard))
	launches(build, "-c")
	// The command execs `<this executable> run`, which is the test binary here, so only check that it exists.
	if info, err := os.Stat(filepath.Join(home, "bin", "pi")); err != nil || info.Mode().Perm()&0111 == 0 {
		t.Fatal("no executable pi command")
	}
	must(setOff(true, io.Discard))
	launches(stock)
	must(setOff(false, io.Discard))
	if exe, wait, notice, _ := in().launch([]string{"install", "npm:x"}); exe != stock || !wait || !strings.Contains(notice, "pitch build") {
		t.Fatalf("package commands go to the original Pi: %s %v %q", exe, wait, notice)
	}
	writeFile(t, stockPackage, `{"version": "1.10.0"}`, 0644)
	launches(stock)
	writeFile(t, stockPackage, `{"version": "1.3.9-beta.1"}`, 0644)
	launches(build)

	must(buildInstalled(context.Background(), "", io.Discard))
	must(buildInstalled(context.Background(), "", io.Discard))
	if ids, _ := in().versions(); len(ids) != 2 || in().current() != ids[1] {
		t.Fatalf("versions: %v, current %s", ids, in().current())
	}
	must(status(io.Discard))
	must(uninstall(io.Discard))
	if exists(home) {
		t.Fatal("uninstall left", home)
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"1.10.0", "1.4.0", true}, {"1.4.0", "1.4.0", false}, {"1.3.9", "1.4.0", false}, {"2.0.0-beta.1", "1.9.9", true}, {"1.4.1", "1.4", true}} {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%s, %s) = %v", c.a, c.b, got)
		}
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
