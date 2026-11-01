package pix

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func integrationArtifact(t *testing.T) string {
	t.Helper()
	root := os.Getenv("PIX_TEST_ARTIFACT")
	if root == "" {
		t.Skip("set PIX_TEST_ARTIFACT to a built distribution")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(root); err != nil {
		t.Fatal(err)
	}
	return root
}
func isolatedEnv(root string) []string {
	return []string{"HOME=" + root, "PI_CODING_AGENT_DIR=" + filepath.Join(root, "agent"), "PATH=/usr/bin:/bin", "PI_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1", "TERM=xterm-256color"}
}

func TestIntegrationExtensions(t *testing.T) {
	artifact := integrationArtifact(t)
	exe := filepath.Join(artifact, "pi")
	home := t.TempDir()
	marker := filepath.Join(home, "marker")
	env := append(isolatedEnv(home), "PIX_TEST_MARKER="+marker)
	auto := fmt.Sprintf("import {appendFileSync} from 'node:fs'; appendFileSync(%s, 'discovered\\n'); export default function() {}", jsString(marker))
	put(t, filepath.Join(home, "agent/extensions/auto.ts"), auto)
	put(t, filepath.Join(home, ".pi/extensions/auto.ts"), auto)
	pkg := filepath.Join(home, "package")
	put(t, filepath.Join(pkg, "package.json"), `{"name":"fixture-package","pi":{"extensions":["index.ts"]}}`)
	put(t, filepath.Join(pkg, "index.ts"), auto)
	settings, _ := json.Marshal(map[string]any{"packages": []string{pkg}})
	put(t, filepath.Join(home, "agent/settings.json"), string(settings))
	var manifest Manifest
	data, err := os.ReadFile(filepath.Join(artifact, "pix-build.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	fixture := false
	for _, extension := range manifest.Extensions {
		if extension.Name == "fixture" || filepath.Base(extension.Entry) == "fixture.ts" {
			fixture = true
		}
	}
	args := []string{"--mode", "rpc", "--no-session", "--offline"}
	result, err := rpcRequest(context.Background(), exe, home, env, args, "get_commands")
	if err != nil {
		t.Fatal(err)
	}
	// Like Pi, pix discovers extensions on disk: the user's extensions directory and the settings package.
	actual, _ := os.ReadFile(marker)
	if strings.Count(string(actual), "discovered") < 2 || (fixture && manifest.ExtensionsAutoLoad && !strings.Contains(string(actual), "module\nfactory\n")) {
		t.Fatalf("default activation: %q", actual)
	}
	if fixture {
		if !manifest.ExtensionsAutoLoad {
			result, err = rpcRequest(context.Background(), exe, home, env, append(args, "-e", "builtin:pix-fixture"), "get_commands")
			if err != nil {
				t.Fatal(err)
			}
		}
		data, err := os.ReadFile(marker)
		if err != nil || !strings.Contains(string(data), "module\nfactory\n") {
			t.Fatalf("compiled activation: %q %v", data, err)
		}
		encoded, _ := json.Marshal(result)
		if !bytes.Contains(encoded, []byte("pix-fixture-test")) {
			t.Fatalf("fixture missing: %s", encoded)
		}
	}
	if exists(marker) {
		if err := os.Remove(marker); err != nil {
			t.Fatal(err)
		}
	}
	explicit := filepath.Join(home, "explicit.ts")
	put(t, explicit, auto)
	if _, err := rpcRequest(context.Background(), exe, home, env, append(args, "-e", explicit), "get_state"); err != nil {
		t.Fatal(err)
	}
	actual, _ = os.ReadFile(marker)
	if !bytes.Contains(actual, []byte("discovered")) {
		t.Fatal("explicit disk extension did not load")
	}
}

func TestIntegrationManagementCommands(t *testing.T) {
	exe := filepath.Join(integrationArtifact(t), "pi")
	home := t.TempDir()
	// Packages are managed as by Pi; Pi itself is updated with pix, not by Pi's self-update.
	for _, args := range [][]string{{"update", "--self"}, {"update", "pi"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, exe, args...)
		cmd.Dir, cmd.Env = home, isolatedEnv(home)
		out, err := cmd.CombinedOutput()
		cancel()
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 2 || !bytes.Contains(out, []byte("Pi is updated with pix")) {
			t.Fatalf("%v: %v %s", args, err, out)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "list")
	cmd.Dir, cmd.Env = home, isolatedEnv(home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("list: %v %s", err, out)
	}
}

func TestIntegrationStreamedWrite(t *testing.T) {
	artifact := integrationArtifact(t)
	for _, malformed := range []bool{false, true} {
		t.Run(fmt.Sprint("malformed=", malformed), func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "result.txt")
			suffix := " τέλος🙂\n"
			content := strings.Repeat("x", 200*1024-len(suffix)) + suffix
			encoded, _ := json.Marshal(map[string]string{"path": target, "content": content})
			argument := string(encoded)
			if malformed {
				argument = `{"path":` + jsString(target) + `,"content":"unfinished`
				put(t, target, "existing content")
			}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" {
					http.Error(w, "POST only", 405)
					return
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, "bad json", 400)
					return
				}
				count := requests.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				send := func(delta map[string]any, finish any) {
					message := map[string]any{"id": "pix-local", "object": "chat.completion.chunk", "created": 1, "model": "local-test", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
					data, _ := json.Marshal(message)
					fmt.Fprintf(w, "data: %s\n\n", data)
				}
				send(map[string]any{"role": "assistant"}, nil)
				if count == 1 {
					send(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "pix-write", "type": "function", "function": map[string]any{"name": "write", "arguments": ""}}}}, nil)
					runes := []rune(argument)
					for i := 0; i < len(runes); i += 64 {
						end := min(i+64, len(runes))
						send(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": string(runes[i:end])}}}}, nil)
					}
					send(map[string]any{}, "tool_calls")
				} else {
					send(map[string]any{"content": "done"}, nil)
					send(map[string]any{}, "stop")
				}
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer server.Close()
			models := map[string]any{"providers": map[string]any{"pix-test": map[string]any{"baseUrl": server.URL + "/v1", "api": "openai-completions", "apiKey": "local-only", "models": []any{map[string]any{"id": "local-test", "name": "Local test", "reasoning": false, "input": []string{"text"}, "contextWindow": 100000, "maxTokens": 100000, "cost": map[string]int{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}}}}}
			data, _ := json.Marshal(models)
			put(t, filepath.Join(root, "agent/models.json"), string(data))
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, filepath.Join(artifact, "pi"), "--offline", "--no-session", "--provider", "pix-test", "--model", "local-test", "-p", "write the test file")
			cmd.Dir, cmd.Env = root, isolatedEnv(root)
			protectProcess(cmd)
			output, err := cmd.CombinedOutput()
			if !malformed && err != nil {
				t.Fatalf("write failed: %v\n%s", err, output)
			}
			actual, readErr := os.ReadFile(target)
			want := content
			if malformed {
				want = "existing content"
			}
			if readErr != nil || string(actual) != want {
				t.Fatalf("file integrity: read=%v got=%d want=%d; process=%v\n%s", readErr, len(actual), len(want), err, output)
			}
			if requests.Load() < 1 {
				t.Fatal("did not reach the local model")
			}
			if !malformed && requests.Load() != 2 {
				t.Fatalf("expected tool loop and final answer: requests=%d output=%s", requests.Load(), output)
			}
		})
	}
}
