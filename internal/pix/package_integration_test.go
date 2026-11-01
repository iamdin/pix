package pix

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestIntegrationPiLoopActivation(t *testing.T) {
	artifact := integrationArtifact(t)
	data, err := os.ReadFile(filepath.Join(artifact, "pix-build.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	var name string
	for _, extension := range manifest.Extensions {
		if extension.Package == "@getpie/pi-loop" {
			name = extension.Name
		}
	}
	if name == "" {
		t.Skip("artifact does not include the pi-loop npm package")
	}
	home := t.TempDir()
	for _, enabled := range []bool{false, true} {
		args := []string{"--mode", "rpc", "--no-session", "--offline"}
		if enabled && !manifest.ExtensionsAutoLoad {
			args = append(args, "-e", "builtin:pix-"+name)
		}
		result, err := rpcRequest(context.Background(), filepath.Join(artifact, "pi"), home, isolatedEnv(home), args, "get_commands")
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		for _, command := range []string{"loop", "loops"} {
			present := bytes.Contains(encoded, []byte(`"name":"`+command+`"`))
			if present != (enabled || manifest.ExtensionsAutoLoad) {
				t.Fatalf("enabled=%v command=%s: %s", enabled, command, encoded)
			}
		}
	}
}
