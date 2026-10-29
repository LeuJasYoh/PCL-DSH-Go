// SPDX-License-Identifier: MIT

//go:build linux

package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEntriesFollowsSymlinkedRuntime guards the integration case where a
// prepared runtime directory is linked into <data-root>/runtimes: Installed()
// already followed the symlink, so Entries/InstalledVersions must follow it too
// (otherwise the API launch precheck wrongly reports "runtime not installed").
func TestEntriesFollowsSymlinkedRuntime(t *testing.T) {
	t.Parallel()
	dataRoot := t.TempDir()
	realRoot := t.TempDir()
	writeRuntimeAt(t, realRoot, "0.2.1-rc.3")
	runtimesDirectory := filepath.Join(dataRoot, "runtimes")
	if err := os.MkdirAll(runtimesDirectory, 0o700); err != nil {
		t.Fatalf("mkdir runtimes: %v", err)
	}
	if err := os.Symlink(filepath.Join(realRoot, "0.2.1-rc.3"), filepath.Join(runtimesDirectory, "0.2.1-rc.3")); err != nil {
		t.Fatalf("symlink runtime: %v", err)
	}
	service := New(nil, nil, dataRoot)
	if !service.Installed("0.2.1-rc.3") {
		t.Fatal("Installed() = false for symlinked runtime")
	}
	entries := service.Entries()
	if len(entries) != 1 || entries[0].Version != "0.2.1-rc.3" {
		t.Fatalf("Entries() = %+v, want the symlinked 0.2.1-rc.3 runtime", entries)
	}
	versions := service.InstalledVersions()
	if len(versions) != 1 || versions[0] != "0.2.1-rc.3" {
		t.Fatalf("InstalledVersions() = %v", versions)
	}
}
