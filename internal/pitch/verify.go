package pitch

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func Verify(directory string) error {
	if exists(filepath.Join(directory, ".pitch-incomplete")) {
		return fmt.Errorf("incomplete build: %s", directory)
	}
	manifestPath := filepath.Join(directory, "pitch-build.json")
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil {
		return err
	}
	if !manifestInfo.Mode().IsRegular() || manifestInfo.Size() > 8<<20 {
		return fmt.Errorf("manifest must be a regular file no larger than 8 MiB")
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return err
	}
	if manifest.Schema != 1 || manifest.Files["pi"] == "" || manifest.Files["package.json"] == "" || manifest.Files["recipe/sources.lock.json"] == "" {
		return fmt.Errorf("invalid artifact manifest")
	}
	actual, err := fileHashes(directory)
	if err != nil {
		return err
	}
	if len(actual) != len(manifest.Files) {
		return fmt.Errorf("artifact file set differs from manifest")
	}
	for path, hash := range manifest.Files {
		if !filepath.IsLocal(path) || path == "." || strings.ContainsRune(path, 92) {
			return fmt.Errorf("unsafe manifest path: %s", path)
		}
		if actual[path] != hash {
			return fmt.Errorf("artifact checksum mismatch: %s", path)
		}
	}
	info, err := os.Stat(filepath.Join(directory, "pi"))
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("pi is not executable")
	}
	return nil
}

func smoke(ctx context.Context, executable, stage, tag string) error {
	home := filepath.Join(stage, "smoke-home")
	if err := os.Mkdir(home, 0700); err != nil {
		return err
	}
	env := []string{"HOME=" + home, "PI_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PATH=/usr/bin:/bin", "PI_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1", "TERM=xterm-256color"}
	// Generous: a machine busy with other work can be many times slower, and extensions load at start.
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	r := runner{ctx, env}
	version, err := r.output(home, executable, "--version")
	if err != nil {
		return err
	}
	if version != strings.TrimPrefix(tag, "v") {
		return fmt.Errorf("wrong Pi version: %s", version)
	}
	help, err := r.output(home, executable, "--help")
	if err != nil {
		return err
	}
	if !strings.Contains(help, "--model") {
		return fmt.Errorf("incomplete Pi help output")
	}
	_, err = rpcRequest(ctx, executable, home, env, []string{"--mode", "rpc", "--no-session", "--offline"}, "get_state")
	return err
}

func rpcRequest(ctx context.Context, executable, dir string, env, args []string, command string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir, cmd.Env = dir, env
	protectProcess(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	input, _ := json.Marshal(map[string]string{"id": "pitch-check", "type": command})
	_, writeErr := stdin.Write(append(input, '\n'))
	var result map[string]any
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	if writeErr == nil {
		for scanner.Scan() {
			var message map[string]any
			if json.Unmarshal(scanner.Bytes(), &message) == nil && message["id"] == "pitch-check" {
				result = message
				break
			}
		}
	}
	cancel()
	_ = stdin.Close()
	_ = cmd.Wait()
	if writeErr != nil {
		return nil, writeErr
	}
	if result == nil {
		return nil, fmt.Errorf("no RPC response: %v; %s", scanner.Err(), stderr.String())
	}
	if result["success"] != true {
		return nil, fmt.Errorf("RPC failed: %v; %s", result, stderr.String())
	}
	return result, nil
}
