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
	"slices"
	"strings"
	"time"
)

// An installation lives in ~/.pitch (PITCH_HOME):
//
//	config.json         the command name and the original Pi it falls back to
//	bin/<name>          the command: a shell script that runs current/pi or the original Pi
//	versions/<id>/      builds; the newest two (and current) are kept for rollback
//	current             symlink to the version in use
//	off                 present while `pitch off` is in effect
type installation struct {
	Home  string `json:"-"`
	Name  string `json:"name"`
	Stock string `json:"stock"`
	// package.json of the original Pi, to compare its version with the build's.
	StockPackage string `json:"stock_package,omitempty"`
	// Shell startup file pitch added its PATH line to, if any.
	RCFile string `json:"rc_file,omitempty"`
}

// Build is replaced in tests.
var buildFunc = Build

const pathMarker = "# added by pitch"

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

func (in *installation) installed() bool { return in.Name != "" }

func (in *installation) command() string { return filepath.Join(in.Home, "bin", in.Name) }

func (in *installation) save() error {
	data, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(in.Home, "config.json"), append(data, '\n'), 0644)
}

// findStock finds the original Pi: the first `pi` on PATH that is not pitch's command.
func findStock(skip string) (string, string) {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		path := filepath.Join(dir, "pi")
		if dir == "" || filepath.Clean(dir) == skip {
			continue
		}
		if info, err := os.Stat(path); err != nil || info.IsDir() || info.Mode().Perm()&0111 == 0 {
			continue
		}
		// npm, Bun and Pi's own release keep package.json at most a few directories above the resolved entry.
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

func ask(stdin *bufio.Reader, stdout io.Writer, question, fallback string) string {
	fmt.Fprint(stdout, question)
	line, _ := stdin.ReadString('\n')
	if line = strings.TrimSpace(line); line != "" {
		return line
	}
	return fallback
}

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func install(ctx context.Context, name string, stdout io.Writer) error {
	in, err := loadInstallation()
	if err != nil {
		return err
	}
	interactive := isTerminal(os.Stdin) && isTerminal(os.Stdout)
	stdin := bufio.NewReader(os.Stdin)
	if name == "" {
		name = in.Name
	}
	if name == "" && interactive {
		name = ask(stdin, stdout, "Command name for the pitch build [pi] (pi replaces your Pi; the original stays and `pitch off` switches back): ", "pi")
	}
	if name == "" {
		name = "pi"
	}
	if !validName.MatchString(name) {
		return fmt.Errorf("invalid command name %q", name)
	}
	if in.installed() && name != in.Name {
		if err := os.Remove(in.command()); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	in.Name = name
	in.Stock, in.StockPackage = findStock(filepath.Join(in.Home, "bin"))
	if in.Stock == "" {
		return fmt.Errorf("Pi is not installed: pitch builds your Pi with its packages, so install Pi first (https://pi.dev)")
	}
	if err := deploy(ctx, in, stdout); err != nil {
		return err
	}
	if path, _ := exec.LookPath(name); path != in.command() {
		if err := addToPath(in, stdin, stdout, interactive); err != nil {
			return err
		}
	}
	fmt.Fprintf(stdout, "\n`%s` now runs the pitch build. The original Pi stays at %s: `pitch off` switches to it, `pitch on` back.\n", name, in.Stock)
	fmt.Fprintln(stdout, "`pitch update` rebuilds after Pi packages change; `pitch status` shows what is installed.")
	return nil
}

// addToPath puts ~/.pitch/bin first on PATH, in the shell startup file if the user agrees.
func addToPath(in *installation, stdin *bufio.Reader, stdout io.Writer, interactive bool) error {
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
		rc = filepath.Join(home, ".bash_profile")
	case "fish":
		rc = filepath.Join(home, ".config", "fish", "config.fish")
		line = fmt.Sprintf("fish_add_path --prepend %q %s", dir, pathMarker)
	}
	if data, err := os.ReadFile(rc); err == nil && strings.Contains(string(data), pathMarker) {
		fmt.Fprintf(stdout, "%s already puts %s first on PATH; open a new terminal.\n", rc, dir)
		return nil
	}
	if rc != "" && interactive && strings.HasPrefix(strings.ToLower(ask(stdin, stdout, fmt.Sprintf("Put %s first on PATH in %s? [Y/n] ", dir, rc), "y")), "y") {
		f, err := os.OpenFile(rc, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := fmt.Fprintf(f, "\n%s\n", line); err != nil {
			return err
		}
		in.RCFile = rc
		fmt.Fprintf(stdout, "Added to %s; open a new terminal (or run: %s).\n", rc, strings.TrimSuffix(line, " "+pathMarker))
		return in.save()
	}
	fmt.Fprintf(stdout, "Add this to your shell startup file to use the pitch build:\n  %s\n", strings.TrimSuffix(line, " "+pathMarker))
	return nil
}

// deploy builds a new version from the current Pi settings and switches to it.
func deploy(ctx context.Context, in *installation, stdout io.Writer) error {
	for _, dir := range []string{"bin", "versions"} {
		if err := os.MkdirAll(filepath.Join(in.Home, dir), 0755); err != nil {
			return err
		}
	}
	opts := Options{Features: defaultFeatures, Out: filepath.Join(in.Home, "versions", time.Now().Format("20060102-150405.000"))}
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
	if err := writeCommand(in); err != nil {
		return err
	}
	if err := in.save(); err != nil {
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

func (in *installation) current() string {
	target, _ := os.Readlink(filepath.Join(in.Home, "current"))
	return filepath.Base(target)
}

// switchTo points current at a version, atomically, so running sessions and new launches never see a gap.
func (in *installation) switchTo(id string) error {
	link := filepath.Join(in.Home, "current")
	tmp := link + ".new"
	_ = os.Remove(tmp)
	if err := os.Symlink(filepath.Join("versions", id), tmp); err != nil {
		return err
	}
	return os.Rename(tmp, link)
}

// prune keeps the current version and the newest other one (for rollback), with their build work directories.
func (in *installation) prune(stdout io.Writer) error {
	ids, err := in.versions()
	if err != nil {
		return err
	}
	current := in.current()
	kept := 0
	for i := len(ids) - 1; i >= 0; i-- {
		if ids[i] == current || kept < 1 {
			if ids[i] != current {
				kept++
			}
			continue
		}
		dir := filepath.Join(in.Home, "versions", ids[i])
		if work := buildWork(dir); work != "" {
			if err := os.RemoveAll(work); err != nil {
				return err
			}
		}
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Removed old version %s\n", ids[i])
	}
	return nil
}

func readManifest(dir string) (Manifest, error) {
	var manifest Manifest
	data, err := os.ReadFile(filepath.Join(dir, "pitch-build.json"))
	if err == nil {
		err = json.Unmarshal(data, &manifest)
	}
	return manifest, err
}

// buildWork is the build's work directory in the pitch cache; compiled extensions load files from it.
func buildWork(dir string) string {
	manifest, err := readManifest(dir)
	if err != nil || !strings.HasPrefix(filepath.Base(manifest.Work), "build-") {
		return ""
	}
	return manifest.Work
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// writeCommand writes the command that users run in place of Pi.
func writeCommand(in *installation) error {
	script := `#!/bin/sh
# Installed by pitch: runs the pitch build of Pi. ` + "`pitch off`" + ` (or PITCH_OFF=1) runs the original Pi instead.
home=` + quote(in.Home) + `
stock=` + quote(in.Stock) + `
version() { sed -n 's/^[[:space:]]*"version":[[:space:]]*"\([^"]*\)".*/\1/p' "$1" 2>/dev/null | head -n 1; }
case "$1" in
install|remove|uninstall|update|config)
	# The original Pi manages packages and itself; the build picks the changes up when rebuilt.
	"$stock" "$@"
	status=$?
	echo 'pitch: run ` + "`pitch update`" + ` to rebuild with this change.' >&2
	exit $status
	;;
esac
if [ -n "$PITCH_OFF" ] || [ -e "$home/off" ]; then
	exec "$stock" "$@"
fi
built=$(version "$home/current/package.json")
original=$(version ` + quote(in.StockPackage) + `)
if [ -n "$original" ] && [ -n "$built" ] && [ "$original" != "$built" ] &&
	[ "$(printf '%s\n%s\n' "$built" "$original" | sort -t. -k1,1n -k2,2n -k3,3n | tail -n 1)" = "$original" ]; then
	echo "pitch: your Pi is $original and this pitch build is $built; running the original Pi until pitch supports $original." >&2
	exec "$stock" "$@"
fi
exec "$home/current/pi" "$@"
`
	tmp := in.command() + ".new"
	if err := os.WriteFile(tmp, []byte(script), 0755); err != nil {
		return err
	}
	return os.Rename(tmp, in.command())
}

func requireInstalled() (*installation, error) {
	in, err := loadInstallation()
	if err == nil && !in.installed() {
		err = fmt.Errorf("pitch is not installed; run `pitch install`")
	}
	return in, err
}

func update(ctx context.Context, stdout io.Writer) error {
	in, err := requireInstalled()
	if err != nil {
		return err
	}
	if err := deploy(ctx, in, stdout); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "\n`%s` now runs the new build; `pitch rollback` returns to the previous one.\n", in.Name)
	return nil
}

func rollback(stdout io.Writer) error {
	in, err := requireInstalled()
	if err != nil {
		return err
	}
	ids, err := in.versions()
	if err != nil {
		return err
	}
	current := in.current()
	for i := len(ids) - 1; i >= 0; i-- {
		if ids[i] != current {
			if err := in.switchTo(ids[i]); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "`%s` now runs version %s (was %s).\n", in.Name, ids[i], current)
			return nil
		}
	}
	return fmt.Errorf("no other version to roll back to")
}

func setOff(off bool, stdout io.Writer) error {
	in, err := requireInstalled()
	if err != nil {
		return err
	}
	marker := filepath.Join(in.Home, "off")
	if off {
		if err := os.WriteFile(marker, nil, 0644); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "`%s` now runs the original Pi (%s); `pitch on` switches back.\n", in.Name, in.Stock)
		return nil
	}
	if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Fprintf(stdout, "`%s` now runs the pitch build.\n", in.Name)
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
	resolved, _ := exec.LookPath(in.Name)
	switch {
	case resolved != in.command():
		fmt.Fprintf(stdout, "Command:  `%s` is not on PATH ahead of others (found %q); add %s first on PATH\n", in.Name, resolved, filepath.Dir(in.command()))
	case exists(filepath.Join(in.Home, "off")):
		fmt.Fprintf(stdout, "Command:  `%s` runs the original Pi (`pitch on` switches back)\n", in.Name)
	default:
		fmt.Fprintf(stdout, "Command:  `%s` runs the pitch build\n", in.Name)
	}
	fmt.Fprintf(stdout, "Build:    Pi %s, %s (%s)\n", strings.TrimPrefix(manifest.Sources.Pi.Tag, "v"), in.current(), manifest.BuiltAt)
	ids := []string{}
	for _, f := range manifest.Features {
		ids = append(ids, f.ID)
	}
	fmt.Fprintf(stdout, "Patches:  %s\n", strings.Join(ids, ", "))
	fmt.Fprintf(stdout, "Plugins:  %d compiled in\n", len(manifest.Extensions))
	for _, e := range manifest.Extensions {
		fmt.Fprintf(stdout, "          %s %s\n", e.Name, e.Version)
	}
	fmt.Fprintf(stdout, "Original: %s\n", in.Stock)
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
		fmt.Fprintln(stdout, "Packages in Pi settings changed since this build: run `pitch update`.")
	}
	ids, err = in.versions()
	if err != nil {
		return err
	}
	if others := slices.DeleteFunc(ids, func(id string) bool { return id == in.current() }); len(others) > 0 {
		fmt.Fprintf(stdout, "Rollback: %s\n", strings.Join(others, ", "))
	}
	return nil
}

func uninstall(stdout io.Writer) error {
	in, err := requireInstalled()
	if err != nil {
		return err
	}
	if in.RCFile != "" {
		data, err := os.ReadFile(in.RCFile)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		var kept []string
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasSuffix(line, pathMarker) {
				kept = append(kept, line)
			}
		}
		if err == nil {
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
		if work := buildWork(filepath.Join(in.Home, "versions", id)); work != "" {
			if err := os.RemoveAll(work); err != nil {
				return err
			}
		}
	}
	if err := os.RemoveAll(in.Home); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Removed %s and `%s`.\n", in.Home, in.Name)
	if in.Name == "pi" {
		fmt.Fprintln(stdout, "`pi` is the original Pi again (in new terminals).")
	}
	return nil
}
