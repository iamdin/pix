package pix

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"time"
)

// `pix` runs Pi from ~/.pix (PIX_HOME):
//
//	versions/<id>/  builds: the current one and the one before (sessions may still run it)
//	current         symlink to the version in use
//
// The first run builds Pi without extensions; Pi loads the user's packages from disk, as the original does.
// `pix compile` builds again with the packages in the Pi settings compiled in.
type installation struct{ Home string }

// Build is replaced in tests.
var buildFunc = Build

// Pi's commands that change the installed packages.
var packageCommands = []string{"install", "remove", "uninstall", "update"}

func pixHome() (string, error) {
	if home := os.Getenv("PIX_HOME"); home != "" {
		return filepath.Abs(home)
	}
	home, err := os.UserHomeDir()
	return filepath.Join(home, ".pix"), err
}

func agentSettings() (string, error) {
	if dir := os.Getenv("PI_CODING_AGENT_DIR"); dir != "" {
		return filepath.Join(dir, "settings.json"), nil
	}
	home, err := os.UserHomeDir()
	return filepath.Join(home, ".pi", "agent", "settings.json"), err
}

func loadInstallation() (*installation, error) {
	home, err := pixHome()
	return &installation{Home: home}, err
}

func (in *installation) current() string {
	target, _ := os.Readlink(filepath.Join(in.Home, "current"))
	if target == "" {
		return ""
	}
	return filepath.Base(target)
}

// compile builds Pi into a new version and switches to it; withPackages compiles in the packages in the Pi settings.
func (in *installation) compile(ctx context.Context, withPackages bool, timeout time.Duration, stdout io.Writer) error {
	if err := os.MkdirAll(filepath.Join(in.Home, "versions"), 0755); err != nil {
		return err
	}
	opts := Options{Out: filepath.Join(in.Home, "versions", time.Now().Format("20060102-150405.000000000")), Timeout: timeout}
	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	opts.Cache = filepath.Join(cache, "pix")
	if withPackages {
		settings, err := agentSettings()
		if err != nil {
			return err
		}
		if packages, err := settingsPackages(settings); err != nil {
			return err
		} else if packages != nil {
			opts.Settings = settings
		}
	}
	if err := buildFunc(ctx, opts, stdout); err != nil {
		return err
	}
	if err := in.switchTo(filepath.Base(opts.Out)); err != nil {
		return err
	}
	return in.prune(stdout)
}

// settingsPackages returns the packages declared in Pi settings, or nil without any.
func settingsPackages(path string) (any, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var settings struct{ Packages any }
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if list, ok := settings.Packages.([]any); !ok || len(list) == 0 {
		return nil, nil
	}
	return settings.Packages, nil
}

func (in *installation) versions() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(in.Home, "versions"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	var ids []string
	for _, entry := range entries {
		if entry.IsDir() {
			ids = append(ids, entry.Name())
		}
	}
	return ids, nil
}

// switchTo points current at a version, atomically.
func (in *installation) switchTo(id string) error {
	link := filepath.Join(in.Home, "current")
	tmp := link + ".new"
	_ = os.Remove(tmp)
	if err := os.Symlink(filepath.Join("versions", id), tmp); err != nil {
		return err
	}
	return os.Rename(tmp, link)
}

// prune keeps the current version and the newest other one, which sessions started before the switch may still run,
// and removes the rest with their build work directories.
func (in *installation) prune(stdout io.Writer) error {
	ids, err := in.versions()
	if err != nil {
		return err
	}
	current := in.current()
	previous := false
	for i := len(ids) - 1; i >= 0; i-- {
		if ids[i] == current {
			continue
		}
		if !previous {
			previous = true
			continue
		}
		if err := removeVersion(filepath.Join(in.Home, "versions", ids[i])); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Removed old build %s\n", ids[i])
	}
	return nil
}

func removeVersion(dir string) error {
	if manifest, err := readManifest(dir); err == nil && strings.HasPrefix(filepath.Base(manifest.Work), "build-") {
		// Compiled extensions load files from the build's work directory in the pix cache.
		if err := os.RemoveAll(manifest.Work); err != nil {
			return err
		}
	}
	return os.RemoveAll(dir)
}

func readManifest(dir string) (Manifest, error) {
	var manifest Manifest
	data, err := os.ReadFile(filepath.Join(dir, "pix-build.json"))
	if err == nil {
		err = json.Unmarshal(data, &manifest)
	}
	return manifest, err
}

// runPi runs Pi with args, building it first on the first run.
func runPi(ctx context.Context, args []string, stderr io.Writer) error {
	in, err := loadInstallation()
	if err != nil {
		return err
	}
	if in.current() == "" {
		fmt.Fprintln(stderr, "pix: building Pi for this machine, once (about a minute)")
		if err := in.compile(ctx, false, 30*time.Minute, stderr); err != nil {
			return err
		}
	}
	exe := filepath.Join(in.Home, "current", "pi")
	manifest, _ := readManifest(filepath.Join(in.Home, "current"))
	if len(args) == 0 || !slices.Contains(packageCommands, args[0]) || len(manifest.Extensions) == 0 {
		return syscall.Exec(exe, append([]string{exe}, args...), os.Environ())
	}
	// The packages change on disk; the compiled-in copies stay until the next `pix compile`.
	cmd := exec.Command(exe, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	runErr := cmd.Run()
	if runErr == nil {
		fmt.Fprintln(stderr, "pix: run `pix compile` to compile this change in.")
	}
	if exit, ok := runErr.(*exec.ExitError); ok {
		os.Exit(exit.ExitCode())
	}
	return runErr
}

func status(stdout io.Writer) error {
	in, err := loadInstallation()
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "pix %s\n", Version)
	if in.current() == "" {
		fmt.Fprintln(stdout, "Build:      none yet; the first `pix` builds it")
		return nil
	}
	current := filepath.Join(in.Home, "versions", in.current())
	manifest, err := readManifest(current)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Build:      Pi %s, built %s\n", strings.TrimPrefix(manifest.Sources.Pi.Tag, "v"), manifest.BuiltAt)
	pi := manifest.Sources.Pi
	fmt.Fprintf(stdout, "Source:     %s %s %.7s\n", strings.TrimSuffix(pi.Repository, ".git"), pi.Branch, pi.Commit)
	if len(manifest.Extensions) == 0 {
		fmt.Fprintln(stdout, "Extensions: loaded from disk, as by Pi (`pix compile` compiles them in)")
		return nil
	}
	fmt.Fprintf(stdout, "Extensions: %d compiled in, the rest loaded from disk\n", len(manifest.Extensions))
	for _, e := range manifest.Extensions {
		fmt.Fprintf(stdout, "            %s %s\n", e.Name, e.Version)
	}
	settings, err := agentSettings()
	if err != nil {
		return err
	}
	now, err := settingsPackages(settings)
	if err != nil {
		return err
	}
	var built struct{ Packages any }
	if data, err := os.ReadFile(filepath.Join(current, "recipe", "extensions.json")); err == nil {
		_ = json.Unmarshal(data, &built)
	}
	if !reflect.DeepEqual(now, built.Packages) {
		fmt.Fprintln(stdout, "Packages in Pi settings changed since this build: run `pix compile`.")
	}
	return nil
}

func uninstall(stdout io.Writer) error {
	in, err := loadInstallation()
	if err != nil {
		return err
	}
	if !exists(in.Home) {
		return fmt.Errorf("nothing to uninstall at %s", in.Home)
	}
	ids, err := in.versions()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := removeVersion(filepath.Join(in.Home, "versions", id)); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(in.Home); err != nil {
		return err
	}
	self, _ := os.Executable()
	fmt.Fprintf(stdout, "Removed %s and its builds. Pi's settings and sessions are kept. To remove pix itself: rm %s\n", in.Home, self)
	return nil
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
