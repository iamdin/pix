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
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"
)

// `pix` runs Pi from ~/.pix (PIX_HOME):
//
//	versions/<id>/  builds: the current one, and older ones while a session still runs them
//	current         symlink to the version in use
//
// Installing pix downloads the Pi that its release compiled; Pi loads the user's packages from disk, as the original
// does. `pix compile` builds again with the packages in the Pi settings compiled in.
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

// compile builds Pi with the packages in the Pi settings compiled in, and switches to it; without packages, it
// switches to the release's Pi.
func (in *installation) compile(ctx context.Context, timeout time.Duration, stdout io.Writer) error {
	settings, err := agentSettings()
	if err != nil {
		return err
	}
	if packages, err := settingsPackages(settings); err != nil {
		return err
	} else if packages == nil {
		return in.install(ctx, stdout)
	}
	if err := os.MkdirAll(filepath.Join(in.Home, "versions"), 0755); err != nil {
		return err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	opts := Options{Out: filepath.Join(in.Home, "versions", time.Now().Format("20060102-150405.000000000")), Cache: filepath.Join(cache, "pix"), Settings: settings, Installed: true, Timeout: timeout}
	if err := buildFunc(ctx, opts, stdout); err != nil {
		return err
	}
	return in.use(filepath.Base(opts.Out), opts.Cache, stdout)
}

// install switches to the Pi that pix's release compiled, which loads every package from disk.
func (in *installation) install(ctx context.Context, stdout io.Writer) error {
	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	cache = filepath.Join(cache, "pix")
	fmt.Fprintf(stdout, "Downloading Pi for pix %s\n", Version)
	archive, err := fetchAsset(ctx, cache, prebuiltAsset())
	if err != nil {
		return err
	}
	versions := filepath.Join(in.Home, "versions")
	if err := os.MkdirAll(versions, 0755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(in.Home, ".download-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if out, err := exec.CommandContext(ctx, "tar", "-xzf", archive, "-C", tmp).CombinedOutput(); err != nil {
		return fmt.Errorf("unpack %s: %v: %s", archive, err, out)
	}
	if err := Verify(tmp); err != nil {
		return err
	}
	if manifest, err := readManifest(tmp); err != nil || manifest.Pix != Version || len(manifest.Extensions) > 0 {
		return fmt.Errorf("%s is not the Pi of pix %s", prebuiltAsset(), Version)
	}
	id := time.Now().Format("20060102-150405.000000000")
	if err := os.Rename(tmp, filepath.Join(versions, id)); err != nil {
		return err
	}
	return in.use(id, cache, stdout)
}

// use switches to version id and removes what only compiling needs and the versions no longer in use.
func (in *installation) use(id, cache string, stdout io.Writer) error {
	if err := in.switchTo(id); err != nil {
		return err
	}
	// Bun, Pi's prepared sources and the downloads are fetched again when needed.
	for _, pattern := range []string{"runtime", "pi-*", "npm"} {
		dirs, _ := filepath.Glob(filepath.Join(cache, pattern))
		for _, dir := range dirs {
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
		}
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

// prune removes every version but the current one, with its build work directory, unless a running Pi still uses it.
func (in *installation) prune(stdout io.Writer) error {
	ids, err := in.versions()
	if err != nil {
		return err
	}
	for _, id := range ids {
		dir := filepath.Join(in.Home, "versions", id)
		if id == in.current() || running(filepath.Join(dir, "pi")) {
			continue
		}
		if err := removeVersion(dir); err != nil {
			return err
		}
		if stdout != nil {
			fmt.Fprintf(stdout, "Removed old build %s\n", id)
		}
	}
	return nil
}

// running reports whether a process runs exe; when that cannot be told, it assumes one does.
func running(exe string) bool {
	err := exec.Command("pgrep", "-f", regexp.QuoteMeta(exe)+"( |$)").Run()
	exit, ok := err.(*exec.ExitError)
	return !ok || exit.ExitCode() != 1
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

// runPi runs Pi with args, downloading it first on the first run.
func runPi(ctx context.Context, args []string, stderr io.Writer) error {
	in, err := loadInstallation()
	if err != nil {
		return err
	}
	if len(args) > 0 && args[0] == "update" {
		var self bool
		if self, args = updateTarget(args); self {
			if err := updateSelf(stderr); err != nil {
				return err
			}
			if args == nil {
				return nil
			}
		}
	}
	if in.current() == "" {
		if err := in.install(ctx, stderr); err != nil {
			return err
		}
	} else if built, _ := readManifest(filepath.Join(in.Home, "current")); built.Pix != Version {
		// A new pix comes with a new Pi; compile the packages into it again if they were.
		var err error
		if len(built.Extensions) > 0 {
			fmt.Fprintf(stderr, "pix: compiling your packages into the Pi of pix %s (about a minute)\n", Version)
			err = in.compile(ctx, 30*time.Minute, stderr)
		} else {
			err = in.install(ctx, stderr)
		}
		if err != nil {
			fmt.Fprintf(stderr, "pix: %v\npix: running the previous Pi\n", err)
		}
	}
	if ids, _ := in.versions(); len(ids) > 1 {
		_ = in.prune(nil) // a build left for sessions that have since ended
	}
	// The version's own path, so that prune can tell it is running.
	exe := filepath.Join(in.Home, "versions", in.current(), "pi")
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
		fmt.Fprintln(stdout, "Build:      none yet; the first `pix` downloads it")
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

// updateTarget splits Pi's `update` arguments into pix itself (pi, self, --self, --all or no target: pix holds Pi) and
// the rest for Pi, nil when nothing is left.
func updateTarget(args []string) (bool, []string) {
	self, other, rest := false, false, []string{"update"}
	for _, a := range args[1:] {
		switch {
		case a == "pi" || a == "self" || a == "--self":
			self = true
		case a == "--all":
			self, other = true, true
			rest = append(rest, "--extensions")
		default:
			other = other || a == "--extensions" || a == "--extension" || a == "--models" || !strings.HasPrefix(a, "-")
			rest = append(rest, a)
		}
	}
	if !other {
		return true, nil
	}
	return self, rest
}

// installer updates pix in place; replaced in tests.
var installer = "curl -fsSL https://raw.githubusercontent.com/iamdin/pix/main/install.sh | sh"

// updateSelf installs the latest pix over this one, with its Pi.
func updateSelf(stderr io.Writer) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return err
	}
	cmd := exec.Command("sh", "-c", installer)
	cmd.Env = append(os.Environ(), "PIX_INSTALL_DIR="+filepath.Dir(self))
	cmd.Stdout, cmd.Stderr = stderr, stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("update pix: %w", err)
	}
	return nil
}
