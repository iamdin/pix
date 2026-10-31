package pitch

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

type stringsFlag []string

func (s *stringsFlag) String() string     { return fmt.Sprint([]string(*s)) }
func (s *stringsFlag) Set(v string) error { *s = append(*s, v); return nil }

type Options struct {
	Cache, Out, Settings string
	Extensions           stringsFlag
	Patches              stringsFlag
	Features             string
}

func Main(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if err := execute(ctx, args, stdout, stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(stderr, "pitch:", err)
		return 1
	}
	return 0
}

func execute(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	in, err := loadInstallation()
	if err != nil {
		return err
	}
	built := in.current() != ""
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") || len(args) == 0 && !built {
		fmt.Fprintln(stdout, "pitch — Pi with your packages, built ahead of time with Bun\n\n  pitch build [--name pi]   build Pi with the packages in your Pi settings (again after they change)\n  pitch status              what is built and what pitch runs\n  pitch off | on            run the original Pi instead | the pitch build again\n  pitch self uninstall      remove the builds and the --name command\n\nEverything else goes to Pi: pitch -c, pitch \"fix the tests\", pitch install npm:x, ...\npitch run <args> passes anything to Pi, even the words above.\nAlso: pitch build --out DIR (a standalone build, see pitch build --help), pitch features.\nExperimental: macOS ARM64 only. Pi and Bun versions are maintained by pitch.")
		if built {
			fmt.Fprintln(stdout, "\n---- Pi ----")
			cmd := exec.Command(filepath.Join(in.Home, "current", "pi"), "--help")
			cmd.Stdout, cmd.Stderr = stdout, stderr
			return cmd.Run()
		}
		return nil
	}
	if len(args) > 0 && args[0] == "--version" {
		fmt.Fprintf(stdout, "pitch %s\n", Version)
		if built {
			fmt.Fprintf(stdout, "Pi %s\n", packageVersion(filepath.Join(in.Home, "current", "package.json")))
		}
		return nil
	}
	// Everything that is not a pitch command goes to Pi, so pitch takes Pi's whole command line.
	if len(args) == 0 || !slices.Contains([]string{"build", "status", "off", "on", "self", "features"}, args[0]) {
		if len(args) > 0 && args[0] == "run" {
			args = args[1:]
		}
		return runPi(args, stderr)
	}
	pins, err := sourcePins()
	if err != nil {
		return err
	}
	switch args[0] {
	case "features":
		f := flag.NewFlagSet("features", flag.ContinueOnError)
		f.SetOutput(stderr)
		asJSON := f.Bool("json", false, "machine-readable ordered catalog and release defaults")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 {
			return fmt.Errorf("usage: pitch features [--json]")
		}
		defaults := []string{}
		selected, err := selectFeatures(defaultFeatures)
		if err != nil {
			return err
		}
		for _, feature := range selected {
			defaults = append(defaults, feature.ID)
		}
		if *asJSON {
			return json.NewEncoder(stdout).Encode(map[string]any{"schema": 1, "pitch": Version, "pi": pins.Pi, "default": defaults, "all": strings.Split(allFeatures, ","), "features": features})
		}
		fmt.Fprintln(stdout, "Default:", defaultFeatures)
		fmt.Fprintln(stdout, "All:", allFeatures)
		for _, feature := range features {
			fmt.Fprintf(stdout, "%s — %s\n", feature.ID, feature.Note)
		}
		return nil
	case "build":
		cache, err := os.UserCacheDir()
		if err != nil {
			return err
		}
		opts := Options{}
		f := flag.NewFlagSet("build", flag.ContinueOnError)
		f.SetOutput(stderr)
		f.StringVar(&opts.Features, "features", defaultFeatures, "all (default), none, or comma-separated IDs")
		f.Var(&opts.Patches, "patch", "trusted local unified diff, applied after built-ins in flag order (repeatable)")
		f.StringVar(&opts.Cache, "cache-dir", filepath.Join(cache, "pitch"), "downloads, pristine sources and retained build logs")
		f.StringVar(&opts.Out, "out", "", "build into this new directory (never overwritten) instead of building what `pitch` runs")
		name := f.String("name", "", "also install a command with this name that runs the build; pi takes the place of your Pi")
		f.StringVar(&opts.Settings, "settings", "", "JSON file with Pi packages declarations; relative sources resolve from this file")
		f.Var(&opts.Extensions, "extension", "append a Pi package source (repeatable); selected compiled extensions load automatically")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 {
			return fmt.Errorf("unexpected arguments: %v", f.Args())
		}
		if opts.Features == "" {
			return fmt.Errorf("--features cannot be empty; use all or none")
		}
		if opts.Out == "" {
			if opts.Settings != "" || len(opts.Extensions) > 0 || len(opts.Patches) > 0 || opts.Features != defaultFeatures {
				return fmt.Errorf("--settings, --extension, --patch and --features need --out; without it pitch builds from your Pi settings")
			}
			return buildInstalled(ctx, *name, stdout)
		}
		if *name != "" {
			return fmt.Errorf("--name installs the build pitch runs; it does not go with --out")
		}
		return Build(ctx, opts, stdout)
	case "self":
		if len(args) != 2 || args[1] != "uninstall" {
			return fmt.Errorf("usage: pitch self uninstall")
		}
		return uninstall(stdout)
	case "status", "off", "on":
		if len(args) != 1 {
			return fmt.Errorf("usage: pitch %s", args[0])
		}
		if args[0] == "status" {
			return status(stdout)
		}
		return setOff(args[0] == "off", stdout)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
