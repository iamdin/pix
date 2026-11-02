package pix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// `pix` runs Pi from ~/.pix (PIX_HOME):
//
//	versions/<id>/  builds: the current one, and older ones while a session still runs them
//	current         symlink to the version in use
//	lock            held while installing, compiling or pruning
//	latest          the version of pix's latest release, checked at most once an hour
//	compile.log     the output of the last compile in the background
//	compile-failed  the packages that failed to compile with this pix, and whether that was shown
//	compiled        a compile in the background succeeded, not yet shown
//	no-compile      `pix compile --none`: compile nothing in the background
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

// errBusy: another pix holds the lock.
var errBusy = errors.New("another pix is installing or compiling")

// lock serializes the changes to PIX_HOME across pix processes, so that none prunes a build that another is still
// compiling or downloading; the lock goes with the process. Without wait it returns errBusy instead of waiting.
func (in *installation) lock(stderr io.Writer, wait bool) (unlock func(), err error) {
	if err := os.MkdirAll(in.Home, 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(in.Home, "lock"), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		if !wait {
			f.Close()
			return nil, errBusy
		}
		fmt.Fprintln(stderr, "pix: waiting for another pix to finish installing or compiling")
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
			f.Close()
			return nil, err
		}
	}
	return func() { f.Close() }, nil
}

// sync makes PIX_HOME run this pix's Pi: the first time and after an update it downloads it. It removes the builds no
// longer in use. Without wait it returns errBusy while
// another pix installs or compiles.
func (in *installation) sync(ctx context.Context, stderr io.Writer, wait bool) error {
	unlock, err := in.lock(stderr, wait)
	if err != nil {
		return err
	}
	defer unlock()
	if in.current() == "" {
		return in.install(ctx, stderr)
	}
	if built, _ := readManifest(filepath.Join(in.Home, "current")); built.Pix != Version {
		return in.install(ctx, stderr) // autoCompile compiles the packages into it again
	}
	if ids, _ := in.versions(); len(ids) > 1 {
		return in.prune(nil) // a build left for sessions that have since ended
	}
	return nil
}

// startBackground starts `pix self <command>` apart from this process, at low priority; replaced in tests.
var startBackground = func(command string) {
	self, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(self, "self", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if cmd.Start() == nil {
		_ = syscall.Setpriority(syscall.PRIO_PROCESS, cmd.Process.Pid, 10) // compiling must not slow Pi down
	}
}

// packagesCompiled reports whether the packages in Pi settings are those compiled into the current build; key names
// them with this pix.
func (in *installation) packagesCompiled() (key string, compiled bool, err error) {
	settings, err := agentSettings()
	if err != nil {
		return "", false, err
	}
	now, err := settingsPackages(settings)
	if err != nil {
		return "", false, err
	}
	data, _ := json.Marshal(now)
	return Version + " " + string(data), reflect.DeepEqual(now, builtPackages(filepath.Join(in.Home, "current"))), nil
}

// builtPackages returns the packages that a build compiled in, as declared.
func builtPackages(build string) any {
	var built struct{ Packages any }
	if data, err := os.ReadFile(filepath.Join(build, "recipe", "extensions.json")); err == nil {
		_ = json.Unmarshal(data, &built)
	}
	if list, ok := built.Packages.([]any); !ok || len(list) == 0 {
		return nil
	}
	return built.Packages
}

// autoCompile compiles the packages in Pi settings into Pi without asking: when they are not those of the current
// build, it starts `pix self auto-compile` in the background, and a later run has them compiled in; until then they
// load from disk, as in Pi. A failed compile is not tried again until the packages or pix change; autoCompile returns
// its notice once. `pix compile --none` turns it off, `pix compile` on again.
func (in *installation) autoCompile() (notice string, started bool) {
	if releaseSums == "" || exists(filepath.Join(in.Home, "no-compile")) {
		return "", false // a development build has no release to compile with
	}
	key, compiled, err := in.packagesCompiled()
	if err != nil {
		return "", false
	}
	if compiled {
		built, _ := readManifest(filepath.Join(in.Home, "current"))
		if os.Remove(filepath.Join(in.Home, "compiled")) == nil && len(built.Extensions) > 0 {
			return "pix compiled your packages into Pi", false
		}
		return "", false
	}
	failed := filepath.Join(in.Home, "compile-failed")
	data, _ := os.ReadFile(failed)
	if lines := strings.Split(string(data), "\n"); lines[0] == key {
		if len(lines) == 1 {
			_ = os.WriteFile(failed, []byte(key+"\nshown"), 0644)
			return "pix could not compile your packages into Pi, so they load from disk: " + filepath.Join(in.Home, "compile.log"), false
		}
		return "", false
	}
	startBackground("auto-compile")
	return "", true
}

// compileInBackground is `pix self auto-compile`: it compiles the packages in Pi settings into Pi unless another pix
// installs or compiles, and records a failure for autoCompile.
func (in *installation) compileInBackground(ctx context.Context) error {
	unlock, err := in.lock(io.Discard, false)
	if err != nil {
		return err
	}
	defer unlock()
	key, compiled, err := in.packagesCompiled()
	if err != nil || compiled || in.current() == "" {
		return err
	}
	log, err := os.Create(filepath.Join(in.Home, "compile.log"))
	if err != nil {
		return err
	}
	defer log.Close()
	failed := filepath.Join(in.Home, "compile-failed")
	if err := in.compile(ctx, 30*time.Minute, log); err != nil {
		fmt.Fprintf(log, "pix: %v\n", err)
		_ = os.WriteFile(failed, []byte(key), 0644)
		return err
	}
	_ = os.Remove(failed)
	return os.WriteFile(filepath.Join(in.Home, "compiled"), nil, 0644)
}

// latestURL redirects to the page of pix's latest release; replaced in tests.
var latestURL = "https://github.com/iamdin/pix/releases/latest"

// releaseNotice returns what Pi shows (as it shows a new Pi) when the latest release, as last checked, is another pix;
// at most once an hour it starts a check in the background. PI_SKIP_VERSION_CHECK and PI_OFFLINE turn it off, as they
// do Pi's check.
func (in *installation) releaseNotice() string {
	if os.Getenv("PI_SKIP_VERSION_CHECK") != "" || os.Getenv("PI_OFFLINE") != "" || releaseSums == "" {
		return "" // releaseSums: a development build has no release
	}
	file := filepath.Join(in.Home, "latest")
	latest, _ := os.ReadFile(file)
	if info, err := os.Stat(file); err != nil || time.Since(info.ModTime()) >= time.Hour {
		_ = os.WriteFile(file, latest, 0644) // the time of this check, also when it fails
		startBackground("check-update")
	}
	if len(latest) == 0 || string(latest) == Version {
		return ""
	}
	link := "https://github.com/iamdin/pix/releases/tag/v" + string(latest)
	if newerVersion(Version, string(latest)) { // the maintainers went back to an earlier release
		return fmt.Sprintf("pix %s is the latest release again (this is %s): run pix update to go back · %s", latest, Version, link)
	}
	return fmt.Sprintf("pix %s is available: run pix update · %s", latest, link)
}

// newerVersion reports whether version a is newer than b; pix versions are dotted numbers.
func newerVersion(a, b string) bool {
	x, y := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(x) && i < len(y); i++ {
		m, errM := strconv.Atoi(x[i])
		n, errN := strconv.Atoi(y[i])
		if errM != nil || errN != nil {
			return x[i] > y[i]
		}
		if m != n {
			return m > n
		}
	}
	return len(x) > len(y)
}

// checkLatest records the version of pix's latest release for releaseNotice.
func (in *installation) checkLatest(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, latestURL, nil)
	if err != nil {
		return err
	}
	client := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	tag := path.Base(resp.Header.Get("Location")) // .../releases/tag/v1.2.3
	if !strings.HasPrefix(tag, "v") {
		return fmt.Errorf("%s: no latest release (%s)", latestURL, resp.Status)
	}
	return os.WriteFile(filepath.Join(in.Home, "latest"), []byte(strings.TrimPrefix(tag, "v")), 0644)
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
			// The new pix compiles the packages into its Pi.
			if _, started := in.autoCompile(); started {
				fmt.Fprintln(stderr, "pix: compiling your packages into the new Pi in the background; until then they load from disk")
			}
			if args == nil {
				return nil
			}
		}
	}
	// While another pix installs or compiles (an update in the background), run the current Pi rather than wait.
	if err := in.sync(ctx, stderr, in.current() == ""); err != nil && !errors.Is(err, errBusy) {
		if in.current() == "" {
			return err
		}
		fmt.Fprintf(stderr, "pix: %v\npix: running the previous Pi\n", err)
	}
	var notices []string
	compileNotice, _ := in.autoCompile()
	for _, notice := range []string{in.releaseNotice(), compileNotice} {
		if notice != "" {
			notices = append(notices, notice)
		}
	}
	env := os.Environ()
	if len(notices) > 0 {
		env = append(env, "PIX_NOTICE="+strings.Join(notices, "\n")) // the entry has Pi show them
	}
	// The version's own path, so that prune can tell it is running.
	exe := filepath.Join(in.Home, "versions", in.current(), "pi")
	if len(args) == 0 || !slices.Contains(packageCommands, args[0]) {
		return syscall.Exec(exe, append([]string{exe}, args...), env)
	}
	// The packages change on disk; they are compiled in again in the background.
	cmd := exec.Command(exe, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr, cmd.Env = os.Stdin, os.Stdout, os.Stderr, env
	runErr := cmd.Run()
	if runErr == nil {
		if _, started := in.autoCompile(); started {
			fmt.Fprintln(stderr, "pix: compiling the packages into Pi in the background; until then they load from disk")
		}
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
	if info, err := os.Stat(filepath.Join(in.Home, "latest")); err == nil {
		latest, _ := os.ReadFile(filepath.Join(in.Home, "latest"))
		if len(latest) == 0 {
			latest = []byte("unknown")
		}
		fmt.Fprintf(stdout, "Latest:     pix %s (last checked %s)\n", latest, info.ModTime().Format("2006-01-02 15:04"))
	}
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
		fmt.Fprintln(stdout, "Extensions: loaded from disk, as by Pi")
	} else {
		fmt.Fprintf(stdout, "Extensions: %d compiled in, the rest loaded from disk\n", len(manifest.Extensions))
		for _, e := range manifest.Extensions {
			fmt.Fprintf(stdout, "            %s %s\n", e.Name, e.Version)
		}
	}
	key, compiled, err := in.packagesCompiled()
	failed, _ := os.ReadFile(filepath.Join(in.Home, "compile-failed"))
	switch {
	case err != nil:
		return err
	case compiled:
	case exists(filepath.Join(in.Home, "no-compile")):
		fmt.Fprintln(stdout, "Compiling:  off (`pix compile` compiles the packages in and turns it on)")
	case strings.Split(string(failed), "\n")[0] == key:
		fmt.Fprintf(stdout, "Compiling:  the packages failed to compile, see %s\n", filepath.Join(in.Home, "compile.log"))
	default:
		fmt.Fprintln(stdout, "Compiling:  the packages in Pi settings changed; pix compiles them in the background")
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

// installer updates pix in place with the latest release's own install.sh; replaced in tests.
var installer = "curl -fsSL https://github.com/iamdin/pix/releases/latest/download/install.sh | sh"

// updateSelf installs the latest pix over this one and switches to its Pi (install.sh runs `pix self install`).
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
