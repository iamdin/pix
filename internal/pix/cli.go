package pix

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
	// Installed compiles the packages where Pi installed them, from Settings (Pi's own), instead of fetching copies.
	Installed bool
}

func Main(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if err := execute(ctx, args, stdout, stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(stderr, "pix:", err)
		return 1
	}
	return 0
}

const usage = `pix — Pi, built ahead of time with Bun, with performance patches

  pix [Pi arguments]          Pi itself: pix, pix -c, pix -p "...", pix install npm:x, pix --help
  pix compile                 compile the packages in your Pi settings into pix, for speed (again after they change)
  pix self status             what pix runs: Pi version, compiled-in extensions
  pix self feedback FILE      send a failed compile's feedback as a GitHub issue, after you confirm
  pix self uninstall          remove ~/.pix and its builds
  pix self version            pix's version

pix uses Pi's settings, packages and sessions (~/.pi). Experimental: macOS ARM64, Linux x64 and ARM64.`

func execute(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 && args[0] == "compile" {
		return compileCommand(ctx, args[1:], stdout, stderr)
	}
	if len(args) == 0 || args[0] != "self" {
		return runPi(ctx, args, stderr)
	}
	args = args[1:]
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Fprintln(stdout, usage)
		return nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	cache = filepath.Join(cache, "pix")
	switch args[0] {
	case "version", "--version":
		fmt.Fprintln(stdout, Version)
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
			return fmt.Errorf("usage: pix self prepare [--repo REPO --commit SHA] [--platform GOOS/GOARCH] OUT.tar.gz")
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
			return fmt.Errorf("usage: pix self feedback [-y] FEEDBACK.md (a failed compile names its file)")
		}
		return feedback(f.Arg(0), *yes, os.Stdin, stdout)
	case "status", "uninstall":
		if len(args) != 1 {
			return fmt.Errorf("usage: pix self %s", args[0])
		}
		if args[0] == "status" {
			return status(stdout)
		}
		return uninstall(stdout)
	default:
		return fmt.Errorf("unknown command %q; see pix self help", "self "+args[0])
	}
}

func compileCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	opts := Options{}
	f := flag.NewFlagSet("compile", flag.ContinueOnError)
	f.SetOutput(stderr)
	f.StringVar(&opts.Pi, "pi", "", "build this Pi (a .tar.gz from `pix self prepare`) instead of the one built into pix")
	f.StringVar(&opts.Cache, "cache-dir", filepath.Join(cache, "pix"), "downloads, pristine sources and retained build logs")
	f.StringVar(&opts.Out, "out", "", "build into this new directory (never overwritten) instead of the Pi that pix runs")
	f.StringVar(&opts.Settings, "settings", "", "with --out: JSON file with Pi packages declarations; relative sources resolve from this file")
	f.Var(&opts.Extensions, "extension", "with --out: append a Pi package source (repeatable)")
	f.DurationVar(&opts.Timeout, "timeout", 30*time.Minute, "stop compiling after this long (0: never)")
	none := f.Bool("none", false, "compile nothing in: Pi loads every package from disk")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", f.Args())
	}
	if opts.Out == "" {
		if opts.Settings != "" || len(opts.Extensions) > 0 || opts.Pi != "" {
			return fmt.Errorf("--settings, --extension and --pi need --out; without it pix compiles the packages in your Pi settings")
		}
		in, err := loadInstallation()
		if err != nil {
			return err
		}
		if err := in.compile(ctx, !*none, opts.Timeout, stdout); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "Done: pix runs the new build.")
		return nil
	}
	if *none {
		return fmt.Errorf("--none does not go with --out")
	}
	if err := Build(ctx, opts, stdout); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Built %s\n", filepath.Join(opts.Out, "pi"))
	return nil
}
