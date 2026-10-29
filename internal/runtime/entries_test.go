// SPDX-License-Identifier: MIT

package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedProbeCache pretends a web-profile probe succeeded for version.
func seedProbeCache(service *Service, version string) {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.capabilities[version+"\x00web"] = capabilityCacheEntry{
		fingerprint:  "seed",
		capabilities: Capabilities{Version: version, Profile: "web", AppOptions: []string{"--port"}},
	}
}

func TestEntries(t *testing.T) {
	t.Parallel()
	dataRoot := t.TempDir()
	runtimesDirectory := filepath.Join(dataRoot, "runtimes")
	writeRuntimeAt(t, runtimesDirectory, "0.2.0")
	writeRuntimeAt(t, runtimesDirectory, "0.2.1-rc.10")
	writeRuntimeAt(t, runtimesDirectory, "0.2.1")
	// Incomplete/broken runtime directories must not be listed.
	if err := os.MkdirAll(filepath.Join(runtimesDirectory, "0.1.0"), 0o700); err != nil {
		t.Fatalf("mkdir incomplete runtime: %v", err)
	}
	brokenPackage := filepath.Join(runtimesDirectory, "0.3.0", "node_modules", "@deepseek-ai", "dsh")
	if err := os.MkdirAll(brokenPackage, 0o700); err != nil {
		t.Fatalf("mkdir broken runtime: %v", err)
	}
	if err := os.WriteFile(filepath.Join(brokenPackage, "package.json"), []byte(`{not json`), 0o600); err != nil {
		t.Fatalf("write broken manifest: %v", err)
	}

	service := New(nil, nil, dataRoot)
	entries := service.Entries()
	if len(entries) != 3 {
		t.Fatalf("Entries() = %+v, want 3 usable runtimes", entries)
	}
	wantVersions := []string{"0.2.1", "0.2.1-rc.10", "0.2.0"}
	for index, entry := range entries {
		if entry.Version != wantVersions[index] {
			t.Fatalf("Entries()[%d].Version = %q, want %q", index, entry.Version, wantVersions[index])
		}
		wantEntry := filepath.Join(runtimesDirectory, entry.Version, "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
		if entry.EntryPath != wantEntry {
			t.Fatalf("Entries()[%d].EntryPath = %q, want %q", index, entry.EntryPath, wantEntry)
		}
		if entry.Compatibility != "unverified" {
			t.Fatalf("Entries()[%d].Compatibility = %q, want unverified before probing", index, entry.Compatibility)
		}
	}

	seedProbeCache(service, "0.2.1-rc.10")
	entries = service.Entries()
	if len(entries) != 3 {
		t.Fatalf("Entries() after probe cache = %+v", entries)
	}
	if entries[1].Compatibility != m0CompatibilityLabel {
		t.Fatalf("probed version compatibility = %q, want %q", entries[1].Compatibility, m0CompatibilityLabel)
	}
	if entries[0].Compatibility != "unverified" || entries[2].Compatibility != "unverified" {
		t.Fatalf("unprobed versions must stay unverified: %+v", entries)
	}
	if got := service.CompatibilityOf("0.2.1-rc.10"); got != m0CompatibilityLabel {
		t.Fatalf("CompatibilityOf() = %q, want %q", got, m0CompatibilityLabel)
	}
	if got := service.CompatibilityOf("0.2.1"); got != "unverified" {
		t.Fatalf("CompatibilityOf(unprobed) = %q, want unverified", got)
	}
}

func TestEntriesEmpty(t *testing.T) {
	t.Parallel()
	service := New(nil, nil, t.TempDir())
	entries := service.Entries()
	if len(entries) != 0 {
		t.Fatalf("Entries() = %+v, want empty", entries)
	}
	if service.CompatibilityOf("0.2.1-rc.3") != "unverified" {
		t.Fatalf("CompatibilityOf() for a missing version must be unverified")
	}
}

func TestEntriesSkipsEntryValidationFailures(t *testing.T) {
	t.Parallel()
	dataRoot := t.TempDir()
	runtimesDirectory := filepath.Join(dataRoot, "runtimes")
	writeRuntimeAt(t, runtimesDirectory, "0.2.1-rc.3")
	// A valid manifest whose declared entry file is missing must be ignored.
	packageDirectory := filepath.Join(runtimesDirectory, "0.2.2", "node_modules", "@deepseek-ai", "dsh")
	if err := os.MkdirAll(packageDirectory, 0o700); err != nil {
		t.Fatalf("mkdir package: %v", err)
	}
	manifest := `{"name":"@deepseek-ai/dsh","version":"0.2.2","bin":{"dsh":"lib/missing.js"}}`
	if err := os.WriteFile(filepath.Join(packageDirectory, "package.json"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	service := New(nil, nil, dataRoot)
	entries := service.Entries()
	if len(entries) != 1 || entries[0].Version != "0.2.1-rc.3" {
		t.Fatalf("Entries() = %+v, want only the valid runtime", entries)
	}
	if !strings.HasSuffix(entries[0].EntryPath, filepath.Join("lib", "bin.js")) {
		t.Fatalf("EntryPath = %q", entries[0].EntryPath)
	}
}
