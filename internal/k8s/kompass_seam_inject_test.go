// KOMPASS SEAM 3: tests — see docs/SPEC.md ADR-001.
package k8s

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// uniqueMarker returns a value generated at RUNTIME so it never appears as a
// source-code literal — otherwise the filesystem scan would trip over its own
// test source. Its only on-disk occurrence would be a real leak.
func uniqueMarker() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "KOMPASSFSSCAN" + hex.EncodeToString(b)
}

func kubeconfigWithMarker(contextName, clusterName, marker string) []byte {
	return []byte(fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: %s
clusters:
- name: %s
  cluster:
    server: https://%s.example.com:6443
    insecure-skip-tls-verify: true
contexts:
- name: %s
  context:
    cluster: %s
    user: u
users:
- name: u
  user:
    token: %s
`, contextName, clusterName, clusterName, contextName, clusterName, marker))
}

// The seam must NEVER write plaintext kubeconfig to any filesystem path. This
// injects a unique marker, exercises the in-memory client build (the "connect"
// path), then scans every writable / tmpfs / temp location for the marker.
func TestKompassInjectNeverTouchesFilesystem(t *testing.T) {
	marker := uniqueMarker()
	if _, err := KompassInject("fs-c1", kubeconfigWithMarker("ctx-a", "cluster-a", marker)); err != nil {
		t.Fatalf("inject: %v", err)
	}
	defer KompassEvict("fs-c1")

	// Build the client config in memory (the disk-free "connect" step).
	if _, _, ok, err := kompassClientConfigFor("fs-c1"); err != nil || !ok {
		t.Fatalf("client config build: ok=%v err=%v", ok, err)
	}

	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	// Covers tmpfs (/dev/shm), all temp dirs, home, cwd, and common roots — the
	// full surface where a plaintext kubeconfig could plausibly land.
	roots := dedupe([]string{
		os.TempDir(), "/tmp", "/var/tmp", "/dev/shm", home, cwd, "/root", "/home", "/app",
	})
	markerBytes := []byte(marker)
	for _, root := range roots {
		if root == "" {
			continue
		}
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil //nolint:nilerr // skip unreadable entries
			}
			if d.IsDir() {
				// Skip large caches / VCS / vendored trees: they cannot hold a
				// kubeconfig the seam wrote, and walking them is slow. The temp,
				// tmpfs, home-config, and cwd write targets are still covered.
				switch d.Name() {
				case ".git", ".cache", "node_modules", "go", "site-packages", "__pycache__", "proc", "sys", "dev":
					if path != "/dev" { // keep scanning /dev so /dev/shm (tmpfs) is reached when passed as a root
						return filepath.SkipDir
					}
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			info, ierr := d.Info()
			if ierr != nil || info.Size() > 4<<20 { // skip large/binary files
				return nil
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			if strings.Contains(string(data), string(markerBytes)) {
				t.Fatalf("SECURITY: kubeconfig marker found on disk at %s", path)
			}
			return nil
		})
	}
}

// Two clusters resolve to distinct in-memory clients keyed by id — no cross
// contamination.
func TestKompassMultiClusterKeying(t *testing.T) {
	if _, err := KompassInject("c1", kubeconfigWithMarker("ctx-a", "cluster-a", "m1")); err != nil {
		t.Fatal(err)
	}
	if _, err := KompassInject("c2", kubeconfigWithMarker("ctx-b", "cluster-b", "m2")); err != nil {
		t.Fatal(err)
	}
	defer KompassEvict("c1")
	defer KompassEvict("c2")

	cfg1, _, _, err := kompassClientConfigFor("c1")
	if err != nil {
		t.Fatal(err)
	}
	cfg2, _, _, err := kompassClientConfigFor("c2")
	if err != nil {
		t.Fatal(err)
	}
	if cfg1.Host == cfg2.Host {
		t.Fatalf("expected distinct hosts per cluster, both = %s", cfg1.Host)
	}
	if !strings.Contains(cfg1.Host, "cluster-a") || !strings.Contains(cfg2.Host, "cluster-b") {
		t.Fatalf("wrong host routing: c1=%s c2=%s", cfg1.Host, cfg2.Host)
	}
}

// Re-injecting a cluster replaces the prior credential in memory (rotation),
// with no filesystem artifact.
func TestKompassRotationReplaces(t *testing.T) {
	if _, err := KompassInject("rot", kubeconfigWithMarker("ctx-a", "old-cluster", "old")); err != nil {
		t.Fatal(err)
	}
	defer KompassEvict("rot")
	cfgOld, _, _, _ := kompassClientConfigFor("rot")
	if !strings.Contains(cfgOld.Host, "old-cluster") {
		t.Fatalf("pre-rotation host wrong: %s", cfgOld.Host)
	}

	if _, err := KompassInject("rot", kubeconfigWithMarker("ctx-b", "new-cluster", "new")); err != nil {
		t.Fatal(err)
	}
	cfgNew, _, _, _ := kompassClientConfigFor("rot")
	if strings.Contains(cfgNew.Host, "old-cluster") {
		t.Fatalf("rotation did not replace old credential: %s", cfgNew.Host)
	}
	if !strings.Contains(cfgNew.Host, "new-cluster") {
		t.Fatalf("post-rotation host wrong: %s", cfgNew.Host)
	}
}

// Eviction removes the credential entirely — no leftover state.
func TestKompassEvictRemoves(t *testing.T) {
	if _, err := KompassInject("ev", kubeconfigWithMarker("ctx-a", "cluster-a", "m")); err != nil {
		t.Fatal(err)
	}
	if !KompassHasInjected("ev") {
		t.Fatal("expected injected before evict")
	}
	KompassEvict("ev")
	if KompassHasInjected("ev") {
		t.Fatal("credential remained after evict")
	}
	if _, _, ok, _ := kompassClientConfigFor("ev"); ok {
		t.Fatal("client config resolvable after evict")
	}
	// And selecting an evicted cluster is rejected.
	if handled, err := kompassSwitchInjected("kompass:ev"); !handled || err == nil {
		t.Fatalf("expected select of evicted cluster to fail: handled=%v err=%v", handled, err)
	}
}

func TestKompassNonInjectedNamePassesThrough(t *testing.T) {
	// A normal (non-"kompass:") context name must NOT be handled by the seam.
	if handled, _ := kompassSwitchInjected("some-normal-context"); handled {
		t.Fatal("seam should not handle non-injected context names")
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
