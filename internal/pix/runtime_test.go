package pix

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestRuntimeDownload(t *testing.T) {
	script := []byte("#!/bin/sh\necho 1.0.0-test\n")
	var archive bytes.Buffer
	w := zip.NewWriter(&archive)
	f, err := w.CreateHeader(&zip.FileHeader{Name: "bun-test/bun", Method: zip.Deflate})
	if err != nil {
		t.Fatal(err)
	}
	f.Write(script)
	w.Close()
	sum := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Write(archive.Bytes())
	}))
	defer server.Close()
	asset := RuntimeAsset{URL: server.URL + "/bun.zip", SHA256: sum(archive.Bytes()), BinarySHA256: sum(script), Member: "bun-test/bun"}
	pin := RuntimePin{Revision: "1.0.0-test", Platforms: map[string]RuntimeAsset{platform: asset}}
	cache := t.TempDir()
	r := runner{context.Background(), buildEnvironment(t.TempDir(), cache)}
	binary, err := getRuntime(r, cache, pin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := getRuntime(r, cache, pin); err != nil || requests.Load() != 1 {
		t.Fatalf("warm cache: %v, %d downloads", err, requests.Load())
	}
	put(t, binary, "tampered compiler")
	if _, err := getRuntime(r, cache, pin); err == nil {
		t.Fatal("accepted modified compiler")
	}
	if data, _ := os.ReadFile(binary); string(data) != "tampered compiler" {
		t.Fatal("silently replaced modified compiler")
	}
	other := t.TempDir()
	asset.SHA256 = sum([]byte("another archive"))
	pin.Platforms[platform] = asset
	if _, err := getRuntime(runner{context.Background(), buildEnvironment(t.TempDir(), other)}, other, pin); err == nil {
		t.Fatal("accepted a download with the wrong hash")
	}
	if exists(filepath.Join(other, "runtime", asset.SHA256, "bun.zip")) {
		t.Fatal("kept a download with the wrong hash")
	}
}
