package pitch

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		fmt.Fprintln(stdout, "pitch — a pinned Pi built with official Bun AOT\n\nInstall:  install [--name pi], update, status, off, on, rollback, uninstall\nBuild:    features, build\nUse pitch <command> --help. Experimental: macOS ARM64 only.\nPi and Bun versions are maintained by pitch, not user-selectable.")
		return nil
	}
	pins, err := sourcePins()
	if err != nil {
		return err
	}
	switch args[0] {
	case "--version":
		if len(args) != 1 {
			return fmt.Errorf("usage: pitch --version")
		}
		fmt.Fprintln(stdout, Version)
		return nil
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
		f.StringVar(&opts.Out, "out", "pitch-dist", "new output directory; never overwritten")
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
		return Build(ctx, opts, stdout)
	case "install":
		f := flag.NewFlagSet("install", flag.ContinueOnError)
		f.SetOutput(stderr)
		name := f.String("name", "", "command name for the pitch build (default: ask, or pi; pi takes the place of your Pi)")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 {
			return fmt.Errorf("usage: pitch install [--name NAME]")
		}
		return install(ctx, *name, stdout)
	case "update", "status", "off", "on", "rollback", "uninstall":
		if len(args) != 1 {
			return fmt.Errorf("usage: pitch %s", args[0])
		}
		switch args[0] {
		case "update":
			return update(ctx, stdout)
		case "status":
			return status(stdout)
		case "rollback":
			return rollback(stdout)
		case "uninstall":
			return uninstall(stdout)
		}
		return setOff(args[0] == "off", stdout)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
