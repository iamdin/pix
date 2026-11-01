package pix

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPiSettingsPackages(t *testing.T) {
	for _, kind := range []string{"string", "filter", "disabled", "autoload-false", "exact-include", "empty", "duplicate", "normalized-duplicate", "invalid-object", "invalid-array", "null", "no-packages"} {
		t.Run(kind, func(t *testing.T) {
			r, pi, stage := packageTestRunner(t)
			dir := t.TempDir()
			pkg := filepath.Join(dir, "package")
			put(t, filepath.Join(pkg, "package.json"), `{"name":"@test/settings","version":"1.0.0","pi":{"extensions":["src/*.ts","!src/private.ts"]}}`)
			for _, name := range []string{"a", "b", "private"} {
				put(t, filepath.Join(pkg, "src", name+".ts"), "throw new Error('selection must not execute modules'); export default function(){}")
			}
			settings := filepath.Join(dir, "config/settings.json")
			source := "../package"
			var declarations any = []any{source}
			want := 2
			valid := true
			switch kind {
			case "filter":
				declarations = []any{map[string]any{"source": source, "extensions": []string{"src/*.ts", "!src/b.ts"}}}
				want = 1
			case "disabled":
				declarations = []any{map[string]any{"source": source, "extensions": []string{}}}
				want = 0
			case "autoload-false":
				declarations = []any{map[string]any{"source": source, "autoload": false}}
				want = 0
			case "exact-include":
				declarations = []any{map[string]any{"source": source, "autoload": false, "extensions": []string{"+src/a.ts"}}}
				want = 1
			case "empty":
				declarations = []any{}
				want = 0
			case "duplicate":
				declarations = []any{source, source}
			case "normalized-duplicate":
				declarations = []any{source, "./" + source}
				source = "./" + source
			case "invalid-object":
				declarations = []any{map[string]any{"source": source, "extensions": true}}
				valid = false
			case "invalid-array":
				declarations = map[string]string{"source": source}
				valid = false
			case "null":
				declarations = nil
				valid = false
			case "no-packages":
				valid = false
			}
			data, err := json.Marshal(map[string]any{"packages": declarations, "npmCommand": []string{"must-not-execute"}, "extensions": []string{"must-not-load"}, "apiKey": "not-copied"})
			if err != nil {
				t.Fatal(err)
			}
			if kind == "no-packages" {
				data = []byte("{}")
			}
			put(t, settings, string(data))
			other := t.TempDir()
			t.Chdir(other)
			var log bytes.Buffer
			selected, err := resolveExtensions(r, "node", pi, stage, nil, settings, &log)
			if !valid {
				if err == nil {
					t.Fatalf("accepted invalid config: %s", data)
				}
				return
			}
			if err != nil || len(selected) != want {
				t.Fatalf("entries=%+v want=%d err=%v\n%s", selected, want, err, &log)
			}
			for _, extension := range selected {
				if filepath.Base(extension.Entry) == "private.ts" || extension.Source != source {
					t.Fatalf("wrong filtered entry/source: %+v", extension)
				}
				if want == 1 && filepath.Base(extension.Entry) != "a.ts" {
					t.Fatal(extension.Entry)
				}
			}
			evidence, err := os.ReadFile(filepath.Join(stage, "extensions.json"))
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(evidence, []byte("not-copied")) || bytes.Contains(evidence, []byte("must-not-execute")) {
				t.Fatal("copied unrelated settings")
			}
			after, err := os.ReadFile(settings)
			if err != nil || !bytes.Equal(data, after) {
				t.Fatal("modified input settings")
			}
		})
	}
}
