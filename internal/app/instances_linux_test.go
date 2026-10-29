//go:build linux

// SPDX-License-Identifier: MIT

package app

import (
	"os"
	"path/filepath"
	"testing"
)

// TestInstanceDirectoryPermissionsLinux verifies the eagerly created instance
// layout uses 0700 permissions (docs/IMPLEMENTATION_SPEC.md §3/§5.13).
func TestInstanceDirectoryPermissionsLinux(t *testing.T) {
	application, _, _, dirs := newTestApp(t)
	instance := mustCreateInstance(t, application, "权限实例", 0)
	instanceDir := filepath.Join(dirs.InstancesDir, instance.ID)

	paths := []string{instanceDir}
	for _, sub := range instanceSubdirectories {
		paths = append(paths, filepath.Join(instanceDir, sub))
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s 失败: %v", path, err)
		}
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Fatalf("%s 权限 = %04o，期望 0700", path, perm)
		}
	}
}
