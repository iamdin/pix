package pix

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteFeedback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stage := filepath.Join(home, "cache", "build-1")
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(stage, 0700))
	must(os.WriteFile(filepath.Join(stage, "build.log"), []byte("compiling "+home+"/x.ts\n"), 0600))
	file, err := writeFeedback(stage, "Fetch extensions", 3*time.Second, errors.New("failed at "+home+"/y"), []Extension{{Name: "ext", Version: "1.0.0", Source: "npm:ext"}})
	must(err)
	data, _ := os.ReadFile(file)
	text := string(data)
	msg := failure(errCompileTime, stage, "Compile", time.Minute, nil).Error()
	if !errors.Is(failure(errCompileTime, stage, "Compile", time.Minute, nil), errCompileTime) || !strings.Contains(msg, "\nSend feedback: pix self feedback "+stage) || !strings.Contains(msg[strings.Index(msg, "If you are an AI agent"):], "pix self feedback -y") || !strings.Contains(msg, "leaving extensions out") || strings.Contains(msg[:strings.Index(msg, "If you are")], "-y") {
		t.Fatal(msg)
	}
	if !strings.HasPrefix(text, "# pix build failed: Fetch extensions\n") || strings.Contains(text, home) || !strings.Contains(text, "~/x.ts") || !strings.Contains(text, "ext 1.0.0 (npm:ext)") {
		t.Fatal(text)
	}
}

func TestFeedback(t *testing.T) {
	file := filepath.Join(t.TempDir(), "feedback.md")
	if err := os.WriteFile(file, []byte("# pix build failed: x\n\nbody\n"), 0600); err != nil {
		t.Fatal(err)
	}
	stdin, err := os.Open(file) // not a terminal, like an agent's
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	gh := func(script string) {
		if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\n"+script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name, script string
		yes          bool
		want         string
	}{
		{"unconfirmed", "", false, "pix self feedback -y"},
		{"unconfirmed, /dev/null", "", false, "pix self feedback -y"},
		{"no gh", "", true, "issues/new?"},
		{"logged out", "exit 1", true, "issues/new?"},
		{"create fails", `[ "$1" = auth ] && exit 0; echo denied >&2; exit 1`, true, "denied"},
		{"sent", `[ "$1" = auth ] && exit 0; cat >/dev/null; echo https://github.com/iamdin/pix/issues/1`, true, "Sent: https://github.com/iamdin/pix/issues/1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.script != "" {
				gh(c.script)
			}
			in := stdin
			if strings.HasSuffix(c.name, "/dev/null") {
				if in, err = os.Open(os.DevNull); err != nil {
					t.Fatal(err)
				}
				defer in.Close()
			}
			var out bytes.Buffer
			err := feedback(file, c.yes, in, &out)
			got := out.String()
			if err != nil {
				got = err.Error()
				if c.yes && !strings.Contains(got, "issues/new?") {
					t.Fatalf("no link to file it by hand: %s", got)
				}
			}
			sent := c.name == "sent" || c.name == "no gh" || c.name == "logged out" // handed over: by gh, or as a link
			if !strings.Contains(got, c.want) || sent != (err == nil) {
				t.Fatalf("%v %s", err, out.String())
			}
		})
	}
}
