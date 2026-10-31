package pitch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestFreshRuntimeSurvivesDeletedUpstreamAsset(t *testing.T) {
	pins, err := sourcePins()
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); http.NotFound(w, r) }))
	defer server.Close()
	pin := pins.Bun
	asset, err := pin.host()
	if err != nil {
		t.Fatal(err)
	}
	asset.URL = server.URL + "/deleted-asset"
	pin.Platforms = map[string]RuntimeAsset{platform: asset}
	cache := t.TempDir()
	r := runner{context.Background(), buildEnvironment(t.TempDir(), cache)}
	binary, err := getRuntime(r, cache, pin)
	if err != nil {
		t.Fatalf("fresh cache must work after the upstream asset is deleted: %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("compiler retrieval contacted deleted upstream %d times", requests.Load())
	}
	if err := requireHash(binary, asset.BinarySHA256); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(cache, "runtime", asset.SHA256, "bun.zip")
	if err := requireHash(archive, asset.SHA256); err != nil {
		t.Fatal(err)
	}
	if _, err := getRuntime(r, cache, pin); err != nil {
		t.Fatalf("warm cache: %v", err)
	}
	put(t, binary, "tampered compiler")
	if _, err := getRuntime(r, cache, pin); err == nil {
		t.Fatal("accepted modified compiler")
	}
	data, _ := os.ReadFile(binary)
	if string(data) != "tampered compiler" {
		t.Fatal("silently replaced modified compiler")
	}
	put(t, archive, "tampered archive")
	if _, err := getRuntime(r, cache, pin); err == nil {
		t.Fatal("accepted modified archive")
	}
	data, _ = os.ReadFile(archive)
	if string(data) != "tampered archive" {
		t.Fatal("silently replaced modified archive")
	}
}
