package pix

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCLIBuildArguments(t *testing.T) {
	var out, stderr bytes.Buffer
	for _, args := range [][]string{{"features"}, {"build", "--features", "all"}, {"build", "--experimental"}, {"build", "--patch", "x.patch", "--out", "x"}, {"prepare"}} {
		out.Reset()
		stderr.Reset()
		if code := Main(context.Background(), args, &out, &stderr); code != 1 {
			t.Fatalf("accepted %v", args)
		}
	}
	out.Reset()
	stderr.Reset()
	if code := Main(context.Background(), []string{"build", "--out", t.TempDir()}, &out, &stderr); code != 1 || !strings.Contains(stderr.String(), "output already exists") {
		t.Fatalf("existing output: %d: %s", code, &stderr)
	}
}
