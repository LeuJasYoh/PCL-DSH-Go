// SPDX-License-Identifier: MIT

package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/apierr"
)

const removeTestVersion = "0.2.1-rc.3"

func TestRemoveInstalledRuntime(t *testing.T) {
	t.Parallel()
	dataRoot := t.TempDir()
	runtimesDirectory := filepath.Join(dataRoot, "runtimes")
	writeRuntimeAt(t, runtimesDirectory, removeTestVersion)
	service := New(nil, nil, dataRoot)
	seedProbeCache(service, removeTestVersion)
	if !service.Installed(removeTestVersion) {
		t.Fatalf("Installed() = false before Remove")
	}

	trashPath, err := service.Remove(removeTestVersion)
	if err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if trashPath == "" {
		t.Fatalf("Remove() returned an empty trash path")
	}
	if _, statErr := os.Stat(filepath.Join(runtimesDirectory, removeTestVersion)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("runtime directory still exists after Remove: %v", statErr)
	}
	info, statErr := os.Stat(trashPath)
	if statErr != nil || !info.IsDir() {
		t.Fatalf("trash path %q is not a directory: %v", trashPath, statErr)
	}
	if !strings.HasPrefix(trashPath, filepath.Join(dataRoot, "trash")) {
		t.Fatalf("trash path %q is not under <dataRoot>/trash", trashPath)
	}
	if base := filepath.Base(trashPath); !strings.HasPrefix(base, "runtime-"+removeTestVersion+"-") {
		t.Fatalf("trash directory base = %q, want runtime-%s-<millis>", base, removeTestVersion)
	}
	if service.Installed(removeTestVersion) {
		t.Fatalf("Installed() = true after Remove")
	}
	if entries := service.Entries(); len(entries) != 0 {
		t.Fatalf("Entries() = %+v after Remove", entries)
	}
	if got := service.CompatibilityOf(removeTestVersion); got != "unverified" {
		t.Fatalf("CompatibilityOf() after Remove = %q, want unverified", got)
	}
	service.mu.Lock()
	_, cached := service.capabilities[removeTestVersion+"\x00web"]
	service.mu.Unlock()
	if cached {
		t.Fatalf("web probe cache was not cleared by Remove")
	}
}

func TestRemoveClearsEveryProfileProbeCache(t *testing.T) {
	t.Parallel()
	dataRoot := t.TempDir()
	writeRuntimeAt(t, filepath.Join(dataRoot, "runtimes"), removeTestVersion)
	service := New(nil, nil, dataRoot)
	seedProbeCache(service, removeTestVersion)
	service.mu.Lock()
	service.capabilities[removeTestVersion+"\x00my-profile"] = capabilityCacheEntry{
		fingerprint:  "seed",
		capabilities: Capabilities{Version: removeTestVersion, Profile: "my-profile", AppOptions: []string{"--port"}},
	}
	otherVersionKey := "0.2.0\x00web"
	service.capabilities[otherVersionKey] = capabilityCacheEntry{fingerprint: "seed", capabilities: Capabilities{Version: "0.2.0"}}
	service.mu.Unlock()

	if _, err := service.Remove(removeTestVersion); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	service.mu.Lock()
	_, webCached := service.capabilities[removeTestVersion+"\x00web"]
	_, profileCached := service.capabilities[removeTestVersion+"\x00my-profile"]
	_, otherCached := service.capabilities[otherVersionKey]
	service.mu.Unlock()
	if webCached || profileCached {
		t.Fatalf("Remove() did not clear every probe cache entry of the version")
	}
	if !otherCached {
		t.Fatalf("Remove() cleared probe cache of a different version")
	}
}

func TestRemoveNotInstalled(t *testing.T) {
	t.Parallel()
	service := New(nil, nil, t.TempDir())
	_, err := service.Remove(removeTestVersion)
	apiErr, ok := apierr.As(err)
	if !ok {
		t.Fatalf("Remove() error = %v, want *apierr.Error", err)
	}
	if apiErr.Status != 404 || apiErr.Code != apierr.CodeNotFound {
		t.Fatalf("Remove() error = %+v, want 404 NOT_FOUND", apiErr)
	}
}

func TestRemoveInvalidVersion(t *testing.T) {
	t.Parallel()
	service := New(nil, nil, t.TempDir())
	for _, version := range []string{"latest", "", "0.2.1/../x"} {
		_, err := service.Remove(version)
		apiErr, ok := apierr.As(err)
		if !ok || apiErr.Status != 404 || apiErr.Code != apierr.CodeNotFound {
			t.Fatalf("Remove(%q) error = %v, want 404 NOT_FOUND", version, err)
		}
	}
}

func TestRemoveCorruptRuntimeIsNotFound(t *testing.T) {
	t.Parallel()
	dataRoot := t.TempDir()
	runtimesDirectory := filepath.Join(dataRoot, "runtimes")
	packageDirectory := filepath.Join(runtimesDirectory, removeTestVersion, "node_modules", "@deepseek-ai", "dsh")
	if err := os.MkdirAll(packageDirectory, 0o700); err != nil {
		t.Fatalf("mkdir corrupt runtime: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packageDirectory, "package.json"), []byte(`{not json`), 0o600); err != nil {
		t.Fatalf("write corrupt manifest: %v", err)
	}
	service := New(nil, nil, dataRoot)
	_, err := service.Remove(removeTestVersion)
	apiErr, ok := apierr.As(err)
	if !ok || apiErr.Status != 404 {
		t.Fatalf("Remove(corrupt) error = %v, want 404 NOT_FOUND", err)
	}
	if _, statErr := os.Stat(filepath.Join(runtimesDirectory, removeTestVersion)); statErr != nil {
		t.Fatalf("corrupt runtime was moved even though it is not installed: %v", statErr)
	}
}
