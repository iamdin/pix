package pitch

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// `pitch build` without --out builds Pi with the user's packages into ~/.pitch (PITCH_HOME), and `pitch pi` runs it:
//
//	config.json         the original Pi to fall back to, and the optional extra command name
//	versions/<id>/      builds: the current one and the one before (sessions may still run it)
//	current             symlink to the version in use
//	off                 present while `pitch off` is in effect
//	bin/<name>          optional command (for example `pi`) that runs `pitch pi`
type installation struct {
	Home string `json:"-"`
	// Extra command for the build, if the user asked for one.
	Name  string `json:"name,omitempty"`
	Stock string `json:"stock,omitempty"`
	// package.json of the original Pi, to compare its version with the build's.
	StockPackage string `json:"stock_package,omitempty"`
	// Shell startup file pitch added its PATH line to, if any.
	RCFile string `json:"rc_file,omitempty"`
}

// Build is replaced in tests.
var buildFunc = Build

const pathMarker = "# added by pitch"

// Pi's own commands that manage packages or Pi itself; the original Pi runs them.
var manageCommands = []string{"install", "remove", "uninstall", "update", "config"}

func pitchHome() (string, error) {
	if home := os.Getenv("PITCH_HOME"); home != "" {
		return filepath.Abs(home)
	}
	home, err := os.UserHomeDir()
	return filepath.Join(home, ".pitch"), err
}

func agentSettings() (string, error) {
	if dir := os.Getenv("PI_CODING_AGENT_DIR"); dir != "" {
		return filepath.Join(dir, "settings.json"), nil
	}
	home, err := os.UserHomeDir()
	return filepath.Join(home, ".pi", "agent", "settings.json"), err
}

func loadInstallation() (*installation, error) {
	home, err := pitchHome()
	if err != nil {
		return nil, err
	}
	in := &installation{Home: home}
	data, err := os.ReadFile(filepath.Join(home, "config.json"))
	if os.IsNotExist(err) {
		return in, nil
	}
	if err != nil {
		return nil, err
	}
	return in, json.Unmarshal(data, in)
}

func requireInstalled() (*installation, error) {
	in, err := loadInstallation()
	if err == nil && in.current() == "" {
		err = fmt.Errorf("no build yet; run `pitch build`")
	}
	return in, err
}

func (in *installation) command() string { return filepath.Join(in.Home, "bin", in.Name) }

func (in *installation) save() error {
	data, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(in.Home, "config.json"), append(data, '\n'), 0644)
}

func (in *installation) current() string {
	target, _ := os.Readlink(filepath.Join(in.Home, "current"))
	if target == "" {
		return ""
	}
	return filepath.Base(target)
}

// findStock finds the original Pi: the first `pi` on PATH that is not pitch's command, and its package.json.
func findStock(skip string) (string, string) {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		path := filepath.Join(dir, "pi")
		if dir == "" || filepath.Clean(dir) == skip {
			continue
		}
		if info, err := os.Stat(path); err != nil || info.IsDir() || info.Mode().Perm()&0111 == 0 {
			continue
		}
		// npm, Bun and Pi's own release keep package.json a few directories above the resolved entry.
		pkg := ""
		if real, err := filepath.EvalSymlinks(path); err == nil {
			for up := filepath.Dir(real); pkg == "" && up != filepath.Dir(up); up = filepath.Dir(up) {
				if data, err := os.ReadFile(filepath.Join(up, "package.json")); err == nil && strings.Contains(string(data), "pi-coding-agent") {
					pkg = filepath.Join(up, "package.json")
				}
			}
		}
		return path, pkg
	}
	return "", ""
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// buildInstalled builds Pi with the packages in the user's Pi settings and makes it what `pitch pi` runs.
func buildInstalled(ctx context.Context, name string, stdout io.Writer) error {
	in, err := loadInstallation()
	if err != nil {
		return err
	}
	if name != "" && !validName.MatchString(name) {
		return fmt.Errorf("invalid command name %q", name)
	}
	if name != "" && in.Name != "" && name != in.Name {
		if err := os.Remove(in.command()); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if name != "" {
		in.Name = name
	}
	in.Stock, in.StockPackage = findStock(filepath.Join(in.Home, "bin"))
	if err := os.MkdirAll(filepath.Join(in.Home, "versions"), 0755); err != nil {
		return err
	}
	opts := Options{Out: filepath.Join(in.Home, "versions", time.Now().Format("20060102-150405.000000000"))}
	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	opts.Cache = filepath.Join(cache, "pitch")
	settings, err := agentSettings()
	if err != nil {
		return err
	}
	if packages, err := settingsPackages(settings); err != nil {
		return err
	} else if packages != nil {
		opts.Settings = settings
	}
	if err := buildFunc(ctx, opts, stdout); err != nil {
		return err
	}
	if err := in.switchTo(filepath.Base(opts.Out)); err != nil {
		return err
	}
	if in.Name != "" {
		if err := writeCommand(in); err != nil {
			return err
		}
	}
	if err := in.save(); err != nil {
		return err
	}
	if err := in.prune(stdout); err != nil {
		return err
	}
	if in.Name == "" {
		fmt.Fprintln(stdout, "Done. Start it with `pitch pi`, which takes the same arguments as `pi` (`pitch pi -c`).")
		return nil
	}
	fmt.Fprintf(stdout, "Done. Start it with `%s` or `pitch pi`.\n", in.Name)
	if path, _ := exec.LookPath(in.Name); path != in.command() {
		if err := addToPath(in, stdout); err != nil {
			return err
		}
	}
	return nil
}

// addToPath puts ~/.pitch/bin first on PATH, in the shell startup file if the user agrees.
func addToPath(in *installation, stdout io.Writer) error {
	dir := filepath.Join(in.Home, "bin")
	line := fmt.Sprintf("export PATH=\"%s:$PATH\" %s", dir, pathMarker)
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	rc := ""
	switch filepath.Base(os.Getenv("SHELL")) {
	case "zsh":
		rc = filepath.Join(home, ".zshrc")
	case "bash":
		// Terminals on macOS start login shells, which read .bash_profile; on Linux they read .bashrc.
		rc = filepath.Join(home, ".bash_profile")
		if runtime.GOOS == "linux" {
			rc = filepath.Join(home, ".bashrc")
		}
	case "fish":
		rc = filepath.Join(home, ".config", "fish", "config.fish")
		line = fmt.Sprintf("fish_add_path --prepend %q %s", dir, pathMarker)
	}
	if data, err := os.ReadFile(rc); err == nil && strings.Contains(string(data), pathMarker) {
		fmt.Fprintf(stdout, "%s already puts %s first on PATH; open a new terminal.\n", rc, dir)
		return nil
	}
	if rc != "" && isTerminal(os.Stdin) {
		fmt.Fprintf(stdout, "Put %s first on PATH in %s? [y/N] ", dir, rc)
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if answer = strings.ToLower(strings.TrimSpace(answer)); strings.HasPrefix(answer, "y") {
			f, err := os.OpenFile(rc, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				return err
			}
			defer f.Close()
			if _, err := fmt.Fprintf(f, "\n%s\n", line); err != nil {
				return err
			}
			in.RCFile = rc
			fmt.Fprintf(stdout, "Added to %s; open a new terminal.\n", rc)
			return in.save()
		}
	}
	fmt.Fprintf(stdout, "Add this to your shell startup file:\n  %s\n", strings.TrimSuffix(line, " "+pathMarker))
	return nil
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
		// Compiled extensions load files from the build's work directory in the pitch cache.
		if err := os.RemoveAll(manifest.Work); err != nil {
			return err
		}
	}
	return os.RemoveAll(dir)
}

func readManifest(dir string) (Manifest, error) {
	var manifest Manifest
	data, err := os.ReadFile(filepath.Join(dir, "pitch-build.json"))
	if err == nil {
		err = json.Unmarshal(data, &manifest)
	}
	return manifest, err
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// writeCommand writes the extra command, which runs `pitch pi`.
func writeCommand(in *installation) error {
	pitch, err := os.Executable()
	if err != nil {
		return err
	}
	if pitch, err = filepath.EvalSymlinks(pitch); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(in.command()), 0755); err != nil {
		return err
	}
	script := "#!/bin/sh\n# Installed by pitch: runs the pitch build of Pi (see `pitch status`).\nexec " + quote(pitch) + " pi \"$@\"\n"
	tmp := in.command() + ".new"
	if err := os.WriteFile(tmp, []byte(script), 0755); err != nil {
		return err
	}
	return os.Rename(tmp, in.command())
}

func packageVersion(path string) string {
	var pkg struct{ Version string }
	data, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(data, &pkg)
	}
	return pkg.Version
}

// newer reports whether version a is newer than b, comparing the numeric parts of major.minor.patch.
func newer(a, b string) bool {
	part := func(v string, i int) int {
		parts := strings.SplitN(v, ".", 3)
		if i >= len(parts) {
			return 0
		}
		digits := strings.TrimRightFunc(parts[i], func(r rune) bool { return r < '0' || r > '9' })
		n, _ := strconv.Atoi(digits)
		return n
	}
	for i := range 3 {
		if x, y := part(a, i), part(b, i); x != y {
			return x > y
		}
	}
	return false
}

// launch decides how `pitch pi` runs Pi: the executable, whether to wait for it (then suggest a rebuild), and a
// notice for the user.
func (in *installation) launch(args []string) (exe string, wait bool, notice string, err error) {
	build := filepath.Join(in.Home, "current", "pi")
	manage := len(args) > 0 && slices.Contains(manageCommands, args[0])
	off := os.Getenv("PITCH_OFF") != "" || exists(filepath.Join(in.Home, "off"))
	if in.Stock == "" && (manage || off) {
		return "", false, "", fmt.Errorf("no original Pi found on PATH (install Pi, then run `pitch build`)")
	}
	switch {
	case manage:
		return in.Stock, true, "pitch: run `pitch build` to rebuild with this change.", nil
	case off:
		return in.Stock, false, "", nil
	}
	if original, built := packageVersion(in.StockPackage), packageVersion(filepath.Join(in.Home, "current", "package.json")); in.Stock != "" && original != "" && built != "" && newer(original, built) {
		return in.Stock, false, fmt.Sprintf("pitch: your Pi is %s and this pitch build is %s; running the original Pi until pitch supports %s.", original, built, original), nil
	}
	return build, false, "", nil
}

func runPi(args []string, stderr io.Writer) error {
	in, err := requireInstalled()
	if err != nil {
		return err
	}
	exe, wait, notice, err := in.launch(args)
	if err != nil {
		return err
	}
	if !wait {
		if notice != "" {
			fmt.Fprintln(stderr, notice)
		}
		return syscall.Exec(exe, append([]string{exe}, args...), os.Environ())
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	runErr := cmd.Run()
	fmt.Fprintln(stderr, notice)
	if exit, ok := runErr.(*exec.ExitError); ok {
		os.Exit(exit.ExitCode())
	}
	return runErr
}

func setOff(off bool, stdout io.Writer) error {
	in, err := requireInstalled()
	if err != nil {
		return err
	}
	marker := filepath.Join(in.Home, "off")
	if off {
		if in.Stock == "" {
			return fmt.Errorf("no original Pi found on PATH")
		}
		if err := os.WriteFile(marker, nil, 0644); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "`pitch pi` now runs the original Pi (%s); `pitch on` switches back.\n", in.Stock)
		return nil
	}
	if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Fprintln(stdout, "`pitch pi` now runs the pitch build.")
	return nil
}

func status(stdout io.Writer) error {
	in, err := requireInstalled()
	if err != nil {
		return err
	}
	current := filepath.Join(in.Home, "versions", in.current())
	manifest, err := readManifest(current)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "pitch %s\n", Version)
	if exists(filepath.Join(in.Home, "off")) {
		fmt.Fprintln(stdout, "Runs:       the original Pi (`pitch on` switches back)")
	} else {
		fmt.Fprintln(stdout, "Runs:       the pitch build")
	}
	if in.Name != "" {
		state := "on PATH"
		if path, _ := exec.LookPath(in.Name); path != in.command() {
			state = fmt.Sprintf("not first on PATH (found %q); put %s first", path, filepath.Dir(in.command()))
		}
		fmt.Fprintf(stdout, "Command:    `%s`, %s\n", in.Name, state)
	}
	fmt.Fprintf(stdout, "Build:      Pi %s, built %s\n", strings.TrimPrefix(manifest.Sources.Pi.Tag, "v"), manifest.BuiltAt)
	pi := manifest.Sources.Pi
	fmt.Fprintf(stdout, "Source:     %s %s %.7s\n", strings.TrimSuffix(pi.Repository, ".git"), pi.Branch, pi.Commit)
	fmt.Fprintf(stdout, "Extensions: %d compiled in\n", len(manifest.Extensions))
	for _, e := range manifest.Extensions {
		fmt.Fprintf(stdout, "            %s %s\n", e.Name, e.Version)
	}
	if in.Stock != "" {
		fmt.Fprintf(stdout, "Original:   %s %s\n", in.Stock, packageVersion(in.StockPackage))
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
		fmt.Fprintln(stdout, "Packages in Pi settings changed since this build: run `pitch build`.")
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
	if in.RCFile != "" {
		if data, err := os.ReadFile(in.RCFile); err == nil {
			var kept []string
			for _, line := range strings.Split(string(data), "\n") {
				if !strings.HasSuffix(line, pathMarker) {
					kept = append(kept, line)
				}
			}
			if err := os.WriteFile(in.RCFile, []byte(strings.Join(kept, "\n")), 0644); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "Removed the PATH line from %s\n", in.RCFile)
		}
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
	fmt.Fprintf(stdout, "Removed %s and its builds.\n", in.Home)
	return nil
}
