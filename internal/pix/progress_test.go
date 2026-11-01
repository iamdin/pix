package pix

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCompileProgressAndTimeout(t *testing.T) {
	heartbeatEvery, longCompile = 20*time.Millisecond, 100*time.Millisecond
	defer func() { heartbeatEvery, longCompile = 5*time.Minute, 10*time.Minute }()
	r := runner{context.Background(), os.Environ()}
	var out bytes.Buffer
	p := &progress{w: &out}
	p.step("Compile")
	err := compile(context.Background(), r, 300*time.Millisecond, p, "", io.Discard, "sleep", "5")
	if !errors.Is(err, errCompileTime) {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "[1/4] Compile\n      still compiling") || !strings.Contains(out.String(), "(longer than usual)") || strings.Contains(out.String(), "\r") {
		t.Fatal(out.String())
	}
	// A terminal keeps a step on one line.
	out.Reset()
	p = &progress{w: &out, tty: true}
	p.step("Compile")
	if err := compile(context.Background(), r, 0, p, "", io.Discard, "sleep", "1.2"); err != nil {
		t.Fatal(err)
	}
	p.end()
	if got := out.String(); strings.Count(got, "\n") != 1 || !strings.Contains(got, "\r[1/4] Compile  1s") {
		t.Fatalf("%q", got)
	}
}
