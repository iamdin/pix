package pitch

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type stringsFlag []string

func (s *stringsFlag) String() string     { return fmt.Sprint([]string(*s)) }
func (s *stringsFlag) Set(v string) error { *s = append(*s, v); return nil }

type Options struct {
	Cache, Out, Settings, Pi string
	Extensions               stringsFlag
	// Timeout stops compiling after this long; 0 never does.
	Timeout time.Duration
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
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Fprintln(stdout, "pitch — Pi with your packages, built ahead of time with Bun\n\n  pitch build [--name pi]   build Pi with the packages in your Pi settings (again after they change)\n  pitch pi [args]           run it; everything after pi goes to Pi: pitch pi -c, pitch pi install npm:x\n  pitch status              what is built and what pitch pi runs\n  pitch off | on            run the original Pi instead | the pitch build again\n  pitch uninstall           remove the builds and the --name command\n  pitch feedback FILE       send a failed build's feedback as a GitHub issue, after you confirm\n\nAlso: pitch build --out DIR (a standalone build, see pitch build --help).\nExperimental: macOS ARM64, Linux x64 and ARM64. Pi and Bun versions are maintained by pitch.")
		return nil
	}
	if args[0] == "pi" {
		return runPi(args[1:], stderr)
	}
	if args[0] == "--version" {
		fmt.Fprintln(stdout, Version)
		return nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	cache = filepath.Join(cache, "pitch")
	switch args[0] {
	case "build":
		opts := Options{}
		f := flag.NewFlagSet("build", flag.ContinueOnError)
		f.SetOutput(stderr)
		f.StringVar(&opts.Pi, "pi", "", "build this Pi (a .tar.gz from `pitch prepare`) instead of the one built into pitch")
		f.StringVar(&opts.Cache, "cache-dir", cache, "downloads, pristine sources and retained build logs")
		f.StringVar(&opts.Out, "out", "", "build into this new directory (never overwritten) instead of building what `pitch pi` runs")
		name := f.String("name", "", "also install a command with this name that runs the build; pi takes the place of your Pi")
		f.StringVar(&opts.Settings, "settings", "", "JSON file with Pi packages declarations; relative sources resolve from this file")
		f.Var(&opts.Extensions, "extension", "append a Pi package source (repeatable); selected compiled extensions load automatically")
		f.DurationVar(&opts.Timeout, "timeout", 30*time.Minute, "stop compiling after this long (0: never)")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 {
			return fmt.Errorf("unexpected arguments: %v", f.Args())
		}
		if opts.Out == "" {
			if opts.Settings != "" || len(opts.Extensions) > 0 || opts.Pi != "" {
				return fmt.Errorf("--settings, --extension and --pi need --out; without it pitch builds from your Pi settings")
			}
			return buildInstalled(ctx, *name, opts.Timeout, stdout)
		}
		if *name != "" {
			return fmt.Errorf("--name installs the build `pitch pi` runs; it does not go with --out")
		}
		if err := Build(ctx, opts, stdout); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Built %s\n", filepath.Join(opts.Out, "pi"))
		return nil
	case "prepare": // for scripts/release.ts and bench/build-variants.ts
		pins, err := sourcePins()
		if err != nil {
			return err
		}
		pinned := pins.Pi.Commit
		f := flag.NewFlagSet("prepare", flag.ContinueOnError)
		f.SetOutput(stderr)
		f.StringVar(&pins.Pi.Repository, "repo", pins.Pi.Repository, "Pi repository (a URL or a local path)")
		f.StringVar(&pins.Pi.Commit, "commit", pins.Pi.Commit, "full commit to build")
		target := f.String("platform", platform, "GOOS/GOARCH the Pi is for")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 1 {
			return fmt.Errorf("usage: pitch prepare [--repo REPO --commit SHA] [--platform GOOS/GOARCH] OUT.tar.gz")
		}
		if !strings.Contains(pins.Pi.Repository, "://") && !filepath.IsAbs(pins.Pi.Repository) {
			return fmt.Errorf("--repo must be a URL or an absolute path")
		}
		if pins.Pi.Commit != pinned {
			pins.Pi.Branch = ""
		}
		return Prepare(ctx, cache, pins.Pi, *target, f.Arg(0), stdout)
	case "feedback":
		f := flag.NewFlagSet("feedback", flag.ContinueOnError)
		f.SetOutput(stderr)
		yes := f.Bool("y", false, "send without asking: only after the user has read the feedback and agreed")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 1 {
			return fmt.Errorf("usage: pitch feedback [-y] FEEDBACK.md (a failed build names its file)")
		}
		return feedback(f.Arg(0), *yes, os.Stdin, stdout)
	case "status", "off", "on", "uninstall":
		if len(args) != 1 {
			return fmt.Errorf("usage: pitch %s", args[0])
		}
		switch args[0] {
		case "status":
			return status(stdout)
		case "uninstall":
			return uninstall(stdout)
		}
		return setOff(args[0] == "off", stdout)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
