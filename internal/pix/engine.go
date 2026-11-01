package pix

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type runner struct {
	ctx context.Context
	env []string
}

func (r runner) command(dir string, log io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(r.ctx, name, args...)
	cmd.Dir, cmd.Env = dir, r.env
	var diagnostic bytes.Buffer
	if log == nil {
		log = &diagnostic
	}
	cmd.Stdout, cmd.Stderr = log, log
	protectProcess(cmd)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w\n%s", name, err, diagnostic.String())
	}
	return nil
}
func (r runner) output(dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(r.ctx, name, args...)
	cmd.Dir, cmd.Env = dir, r.env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	protectProcess(cmd)
	data, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s: %w\n%s", name, err, stderr.String())
	}
	return strings.TrimSpace(string(data)), nil
}
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func hashBytes(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func requireHash(path, expected string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file: %s", path)
	}
	actual, err := hashFile(path)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("checksum mismatch: %s", path)
	}
	return nil
}
func exists(path string) bool { _, err := os.Lstat(path); return err == nil || !os.IsNotExist(err) }
func copyFile(source, dest string, mode fs.FileMode) error {
	src, err := os.Open(source)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
func copyTree(source, dest string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing non-regular artifact file: %s", path)
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}
func fileHashes(root string) (map[string]string, error) {
	hashes := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("non-regular artifact file: %s", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "pix-build.json" {
			return nil
		}
		hash, err := hashFile(path)
		if err != nil {
			return err
		}
		hashes[filepath.ToSlash(rel)] = hash
		return nil
	})
	return hashes, err
}

func download(ctx context.Context, url, target, expected string) error {
	if exists(target) {
		return requireHash(target, expected)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/octet-stream")
	client := http.Client{Timeout: 5 * time.Minute}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", url, response.Status)
	}
	f, err := os.CreateTemp(filepath.Dir(target), "download-*.part")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	const limit = 256 << 20
	n, copyErr := io.Copy(f, io.LimitReader(response.Body, limit+1))
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n > limit {
		return fmt.Errorf("download exceeds 256 MiB")
	}
	if err := requireHash(f.Name(), expected); err != nil {
		return err
	}
	// Link publishes without replacing a concurrent download or an existing user file.
	if err := os.Link(f.Name(), target); err != nil {
		if !os.IsExist(err) {
			return err
		}
		return requireHash(target, expected)
	}
	return nil
}
func extractRuntime(archive, destination, member string) error {
	z, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer z.Close()
	var selected *zip.File
	for _, entry := range z.File {
		if entry.Name == member {
			if selected != nil || !entry.Mode().IsRegular() || entry.UncompressedSize64 > 256<<20 {
				return fmt.Errorf("invalid runtime archive member: %s", entry.Name)
			}
			selected = entry
		}
	}
	if selected == nil {
		return fmt.Errorf("runtime missing %s", member)
	}
	src, err := selected.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(dst, io.LimitReader(src, 256<<20))
	closeErr := dst.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
func getRuntime(r runner, cache string, pin RuntimePin) (string, error) {
	asset, err := pin.host()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cache, "runtime", asset.SHA256)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	archive := filepath.Join(dir, "bun.zip")
	if err := cacheRuntimeArchive(r.ctx, archive, asset.SHA256); err != nil {
		return "", err
	}
	binary := filepath.Join(dir, "bun")
	if !exists(binary) {
		temp, err := os.MkdirTemp(dir, "extract-")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(temp)
		candidate := filepath.Join(temp, "bun")
		if err := extractRuntime(archive, candidate, asset.Member); err != nil {
			return "", err
		}
		if err := requireHash(candidate, asset.BinarySHA256); err != nil {
			return "", err
		}
		if err := os.Link(candidate, binary); err != nil && !os.IsExist(err) {
			return "", err
		}
	}
	if err := requireHash(binary, asset.BinarySHA256); err != nil {
		return "", err
	}
	revision, err := r.output("", binary, "--revision")
	if err != nil {
		return "", err
	}
	if revision != pin.Revision {
		return "", fmt.Errorf("unexpected Bun revision: %s", revision)
	}
	return binary, nil
}

func checkout(r runner, cache string, pin Source, log io.Writer) (string, error) {
	path := filepath.Join(cache, "pi-"+pin.Commit)
	if !exists(path) {
		if err := os.Mkdir(path, 0700); err != nil {
			return "", err
		}
		for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", pin.Repository}, {"fetch", "--depth", "1", "origin", pin.Commit}, {"checkout", "--detach", "FETCH_HEAD"}} {
			if err := r.command(path, log, "git", args...); err != nil {
				return "", err
			}
		}
	}
	commit, err := r.output(path, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if commit != pin.Commit {
		return "", fmt.Errorf("wrong cached commit: %s; not reset", path)
	}
	status, err := r.output(path, "git", "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return "", err
	}
	if status != "" {
		return "", fmt.Errorf("modified cached checkout: %s; not reset", path)
	}
	return path, nil
}
func verifySource(r runner, source, tree string) error {
	actual, err := r.output(source, "git", "write-tree")
	if err != nil {
		return err
	}
	unstaged, err := r.output(source, "git", "diff", "--name-only")
	if err != nil {
		return err
	}
	untracked, err := r.output(source, "git", "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return err
	}
	if actual != tree || unstaged != "" || untracked != "" {
		return fmt.Errorf("source changed beyond recorded patches: %s", source)
	}
	return nil
}
func buildEnvironment(home, cache string) []string {
	env := []string{"HOME=" + home, "PI_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PI_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1", "TERM=xterm-256color", "LANG=en_US.UTF-8", "npm_config_cache=" + filepath.Join(cache, "npm"), "PATH=" + os.Getenv("PATH"), "GIT_TERMINAL_PROMPT=0"}
	for _, key := range []string{"TMPDIR", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy", "SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return env
}
